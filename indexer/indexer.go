package indexer

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"os"
	"strings"
	"sync"
	"time"

	sdkConfig "github.com/evaafi/evaa-go-sdk/config"
	sdkPrincipal "github.com/evaafi/evaa-go-sdk/principal"
	"github.com/evaafi/go-indexer/config"
	"github.com/xssnick/tonutils-go/address"
	"github.com/xssnick/tonutils-go/tvm/cell"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type FutureUpdate struct {
	Address         string
	ContractAddress string
	SubaccountID    int16
	CreatedAt       int64
	Pool            config.Pool
	TxUtime         int64
	// IsRecheck is set on the one-shot follow-up enqueue we schedule after a
	// fresh tx whose post-fetch state matched what was already in DB (likely
	// TonCenter hadn't indexed the new state yet). Limits the recursion to
	// exactly one extra attempt — see scheduleRecheck.
	IsRecheck bool
}

type MapKey struct {
	Address      string
	PoolName     string
	SubaccountID int16
}

// Two-tier work queue.
//
//   - freshQueue:   FutureUpdates produced by indexer-tx events. Workers always
//     drain this first.
//   - reindexQueue: FutureUpdates produced by background sweeps and retries.
//     Workers only pull from this queue when freshQueue is empty.
//
// updateMap dedupes by (wallet, pool, sub) and stores the LATEST FutureUpdate
// known for that key. A fresh-tx producer overwrites the map and pushes to
// freshQueue regardless of whether a reindex entry was already queued; the
// stale reindex entry becomes a "ghost" that the worker filters out via
// processOrSkip (compares the popped value to map's current value — if they
// don't match, skip).
var (
	updateMap    sync.Map
	freshQueue                 = make(chan FutureUpdate, 5000)
	reindexQueue               = make(chan FutureUpdate, 30000)
	sleepTime    time.Duration = 4
	Shutdown                   = make(chan struct{})
	WG           sync.WaitGroup
)

const (
	queueFile = "update_queue.json"
	// updateDelayBufferSeconds: skip the finalization wait if the tx happened
	// more than this long ago. Should equal sleepTime — together they say "if
	// tx is fresher than 4s, sleep until it has had 4s to finalize".
	updateDelayBufferSeconds int64 = 4
	// reindexInterval: how often we sweep all known users for state refresh.
	// Lowered from 4h → 2h now that fresh tx events run on a separate
	// priority queue and can't be starved by reindex bursts.
	reindexInterval = 2 * time.Hour
	// reindexEnqueueDelay: throttle between sweep enqueues so reindex doesn't
	// fire a "lavina" of 10000 entries at once and saturate the TonCenter
	// rate budget. At 250ms we cap reindex at 4 enqueues/sec, leaving the
	// rest of the limiter budget for fresh tx events.
	reindexEnqueueDelay = 250 * time.Millisecond
	// recheckDelay: after a fresh tx's first apply found no change vs the
	// existing DB row (likely TonCenter still serving stale state), wait this
	// long and try once more. Capped at one retry by FutureUpdate.IsRecheck.
	recheckDelay        = 4 * time.Second
	logsInsertBatchSize = 1000
)

func mapKeyOf(fut *FutureUpdate) MapKey {
	return MapKey{Address: fut.Address, PoolName: fut.Pool.Name, SubaccountID: fut.SubaccountID}
}

func SaveQueue() error {
	var queueData []FutureUpdate

	updateMap.Range(func(key, value interface{}) bool {
		queueData = append(queueData, value.(FutureUpdate))
		return true
	})

	data, err := json.Marshal(queueData)
	if err != nil {
		return fmt.Errorf("error encoding JSON: %w", err)
	}

	if err := os.WriteFile(queueFile, data, 0644); err != nil {
		return fmt.Errorf("error writing to file: %w", err)
	}

	fmt.Println("Queue successfully saved")
	return nil
}

func LoadQueue() error {
	if _, err := os.Stat(queueFile); os.IsNotExist(err) {
		fmt.Println("Queue file not found, loading an empty queue")
		return nil
	}

	data, err := os.ReadFile(queueFile)
	if err != nil {
		return fmt.Errorf("error reading file: %w", err)
	}

	var queueData []FutureUpdate
	if err := json.Unmarshal(data, &queueData); err != nil {
		return fmt.Errorf("error decoding JSON: %w", err)
	}

	// Saved entries are stale by definition — restore them at low priority so
	// they don't block fresh tx events from being picked up first.
	for _, fut := range queueData {
		updateMap.Store(mapKeyOf(&fut), fut)
		reindexQueue <- fut
	}

	fmt.Printf("Loaded %d elements into the queue\n", len(queueData))
	return nil
}

func RunIndexer(ctx context.Context, cfg config.Config) {
	if !cfg.ForceResyncOnEveryStart {
		LoadQueue()
	}

	for i := 0; i < cfg.UserSyncWorkers; i++ {
		fmt.Printf("starting user state worker %d/%d\n", i+1, cfg.UserSyncWorkers)
		WG.Add(1)
		go worker(ctx, &WG)
	}

	for _, pool := range config.Pools {
		fmt.Printf("starting %s indexer \n", pool.Name)
		go corutineIndexer(ctx, cfg, pool)
	}

	go startReindexScheduler(ctx)
}

// resolveSDKConfig returns the SDK config + principal service for a pool. Both
// can be nil if the pool name is unknown.
func resolveSDKConfig(poolName string) (*sdkConfig.Config, *sdkPrincipal.Service) {
	switch poolName {
	case "main":
		c := sdkConfig.GetMainMainnetConfig()
		return c, sdkPrincipal.NewService(c)
	case "lp":
		c := sdkConfig.GetLpMainnetConfig()
		return c, sdkPrincipal.NewService(c)
	case "alts":
		c := sdkConfig.GetAltsMainnetConfig()
		return c, sdkPrincipal.NewService(c)
	case "stable":
		c := sdkConfig.GetStableMainnetConfig()
		return c, sdkPrincipal.NewService(c)
	}
	return nil, nil
}

// applyUserStateBoc decodes a base64 BOC of the user-SC storage data and
// upserts the corresponding OnchainUser row. Returns changed=true if the
// freshly parsed state differs from what was already in DB (or the row is new),
// changed=false if the parsed state semantically matches the existing row —
// caller can use this signal to schedule a single recheck after a fresh tx, in
// case TonCenter served stale state.
//
// txUtime is used as updated_at; created_at is preserved by the ON CONFLICT
// clause.
func applyUserStateBoc(pool config.Pool, walletAddress, contractAddress string, subaccountID int16, dataBocBase64 string, txUtime int64) (bool, error) {
	db, _ := config.GetDBInstance()

	decoded, err := base64.StdEncoding.DecodeString(dataBocBase64)
	if err != nil {
		return false, fmt.Errorf("decode base64 user state: %w", err)
	}
	dataCell, err := cell.FromBOC(decoded)
	if err != nil {
		return false, fmt.Errorf("parse user state BOC: %w", err)
	}

	sdkPoolConfig, service := resolveSDKConfig(pool.Name)
	if sdkPoolConfig == nil {
		return false, fmt.Errorf("unknown pool: %s", pool.Name)
	}

	if contractAddress == "" {
		userContractAddress, _ := service.CalculateUserSCAddress(address.MustParseAddr(walletAddress))
		contractAddress = userContractAddress.String()
	}

	userSC := sdkPrincipal.NewUserSC(address.MustParseAddr(contractAddress))
	if _, err := userSC.SetAccData(dataCell); err != nil {
		return false, fmt.Errorf("set acc data: %w", err)
	}

	updatedAt := time.Unix(txUtime, 0)
	user := config.OnchainUser{
		WalletAddress:   walletAddress,
		Pool:            pool.Name,
		SubaccountID:    subaccountID,
		ContractAddress: contractAddress,
		CodeVersion:     int(userSC.CodeVersion()),
		CreatedAt:       updatedAt, // only used on insert; ON CONFLICT skips it on update
		UpdatedAt:       updatedAt,
		State:           config.BigInt{Int: big.NewInt(userSC.UserState())},
		Principals:      buildPrincipals(userSC.Principals(), sdkPoolConfig),
	}

	// Snapshot the current row (if any) before upsert so we can tell if our
	// fetch produced anything new.
	var prev config.OnchainUser
	prevErr := db.Where("wallet_address = ? AND pool = ? AND subaccount_id = ? AND contract_address = ?",
		walletAddress, pool.Name, subaccountID, contractAddress).First(&prev).Error
	isNew := errors.Is(prevErr, gorm.ErrRecordNotFound)
	if prevErr != nil && !isNew {
		return false, fmt.Errorf("read previous user row: %w", prevErr)
	}

	if err := upsertOnchainUser(db, user); err != nil {
		return false, err
	}
	if isNew {
		return true, nil
	}
	return !sameUserState(prev, user), nil
}

// sameUserState compares the fields that change when a user actually does
// something on-chain (state code, principals, code version). Created/updated
// timestamps are ignored — they always change on each upsert and aren't a
// signal of real on-chain change.
func sameUserState(a, b config.OnchainUser) bool {
	if a.CodeVersion != b.CodeVersion {
		return false
	}
	if cmpBigInt(a.State, b.State) != 0 {
		return false
	}
	if len(a.Principals) != len(b.Principals) {
		return false
	}
	for k, av := range a.Principals {
		bv, ok := b.Principals[k]
		if !ok || cmpBigInt(av, bv) != 0 {
			return false
		}
	}
	return true
}

// cmpBigInt compares two BigInt wrappers, treating nil .Int as zero so we
// don't blow up on partially-loaded rows.
func cmpBigInt(a, b config.BigInt) int {
	ai := a.Int
	bi := b.Int
	if ai == nil {
		ai = big.NewInt(0)
	}
	if bi == nil {
		bi = big.NewInt(0)
	}
	return ai.Cmp(bi)
}

func buildPrincipals(raw map[string]*big.Int, poolConfig *sdkConfig.Config) config.Principals {
	out := make(config.Principals, len(poolConfig.Assets))
	for _, asset := range poolConfig.Assets {
		v := big.NewInt(0)
		if r, ok := raw[asset.ID.String()]; ok && r != nil {
			v = new(big.Int).Set(r)
		}
		out[config.BigInt{Int: new(big.Int).Set(asset.ID)}] = config.BigInt{Int: v}
	}
	return out
}

// upsertOnchainUser performs INSERT ... ON CONFLICT DO UPDATE on the full PK
// (wallet_address, pool, subaccount_id, contract_address). created_at is
// intentionally absent from the update set so historical first-seen times stay.
func upsertOnchainUser(db *gorm.DB, u config.OnchainUser) error {
	return db.Clauses(clause.OnConflict{
		Columns: []clause.Column{
			{Name: "wallet_address"},
			{Name: "pool"},
			{Name: "subaccount_id"},
			{Name: "contract_address"},
		},
		DoUpdates: clause.AssignmentColumns([]string{
			"code_version", "updated_at", "state", "principals",
		}),
	}).Create(&u).Error
}

// Stable poll cadence: every 500ms regardless of whether the last cycle
// returned anything. Predictable rate budget, predictable worst-case latency
// on the indexer side (~250ms average, ~500ms tail).
const cycleSleep = 500 * time.Millisecond

func corutineIndexer(ctx context.Context, cfg config.Config, pool config.Pool) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-Shutdown:
			return
		default:
		}

		if err := processIndex(ctx, cfg, pool); err != nil {
			fmt.Printf("indexer %s: %v\n", pool.Name, err)
		}

		select {
		case <-ctx.Done():
			return
		case <-Shutdown:
			return
		case <-time.After(cycleSleep):
		}
	}
}

func processIndex(ctx context.Context, cfg config.Config, pool config.Pool) error {
	db, _ := config.GetDBInstance()

	var state config.OnchainSyncState
	if err := db.Where("pool = ?", pool.Name).First(&state).Error; err != nil {
		return fmt.Errorf("get sync state for pool %s: %w", pool.Name, err)
	}

	pageSize := cfg.MaxPageSize
	if pageSize <= 0 {
		pageSize = 1000
	}

	transactions, lastUtime, lastLt, err := ProcessTransactions(ctx, pool.Address, state.LastUtime, state.LastLt, pageSize)
	if err != nil {
		return fmt.Errorf("fetch transactions for %s from utime=%d lt=%d: %w", pool.Name, state.LastUtime, state.LastLt, err)
	}

	// Empty just means caught up; the LT cursor is exact so we don't have to
	// advance manually. Outer loop will sleep its usual cycle and retry.
	if len(transactions) == 0 {
		return nil
	}

	fmt.Printf("indexer %s got %d new messages (lt %d -> %d)\n", pool.Name, len(transactions), state.LastLt, lastLt)

	state.LastUtime = lastUtime
	state.LastLt = lastLt
	if err := db.Save(&state).Error; err != nil {
		return fmt.Errorf("save sync state for %s: %w", pool.Name, err)
	}

	logs := make([]config.OnchainLog, 0, len(transactions))
	for _, tr := range transactions {
		logVersion := 1
		if pool.Name == "lp" && tr.LT < 49712577000001 || pool.Name == "main" && tr.LT < 49828980000001 {
			logVersion = 0
		}

		for _, body := range tr.OutMsgBodies {
			idxLog, err := ParseLogMessage(body, logVersion)
			if err != nil {
				if strings.Contains(err.Error(), "unknown log type") {
					continue
				}
				fmt.Printf("indexer %s: parse log hash=%s: %v\n", pool.Name, tr.Hash, err)
				continue
			}

			idxLog.Pool = pool.Name
			idxLog.CreatedAt = time.Unix(idxLog.Utime, 0)
			idxLog.Hash = tr.Hash
			logs = append(logs, idxLog)

			fut := FutureUpdate{
				Address:         idxLog.UserAddress,
				ContractAddress: idxLog.SenderAddress,
				SubaccountID:    idxLog.SubaccountID,
				CreatedAt:       time.Now().Unix(),
				Pool:            pool,
				TxUtime:         idxLog.Utime,
			}
			// Always Store-overwrite for fresh: if a stale reindex entry was in
			// the map, we replace it so the previously queued reindex item
			// becomes a ghost (skipped in processOrSkip), while this fresh
			// entry rides the high-priority freshQueue.
			updateMap.Store(mapKeyOf(&fut), fut)
			select {
			case freshQueue <- fut:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}

	for i := 0; i < len(logs); i += logsInsertBatchSize {
		end := i + logsInsertBatchSize
		if end > len(logs) {
			end = len(logs)
		}
		if err := db.Clauses(clause.OnConflict{DoNothing: true}).Create(logs[i:end]).Error; err != nil {
			return fmt.Errorf("insert logs for %s: %w", pool.Name, err)
		}
	}

	return nil
}

// worker reads work units with prefer-fresh priority: it always tries to pop
// from freshQueue first (non-blocking), falling back to a blocking select on
// both queues when fresh is empty. Each popped entry passes through
// processOrSkip so that ghost entries (queued by reindex but later superseded
// by a fresh tx for the same user) are dropped without doing a TonCenter call.
func worker(ctx context.Context, wg *sync.WaitGroup) {
	defer wg.Done()
	for {
		// 1. Try fresh first, non-blocking. This makes prefer-fresh strict:
		//    if both queues have items ready, fresh always wins.
		select {
		case <-ctx.Done():
			return
		case <-Shutdown:
			return
		case fut := <-freshQueue:
			processOrSkip(ctx, &fut)
			continue
		default:
		}

		// 2. Both empty — block on either, plus shutdown signals.
		select {
		case <-ctx.Done():
			return
		case <-Shutdown:
			return
		case fut := <-freshQueue:
			processOrSkip(ctx, &fut)
		case fut := <-reindexQueue:
			processOrSkip(ctx, &fut)
		}
	}
}

// processOrSkip drops the popped entry if it's a ghost — i.e. the dedup map
// either no longer holds an entry for this key (already processed) or holds a
// different value (a fresher producer overwrote us). This is what lets a fresh
// tx producer overwrite a stale reindex entry without leaving duplicate work.
func processOrSkip(ctx context.Context, fut *FutureUpdate) {
	v, ok := updateMap.Load(mapKeyOf(fut))
	if !ok {
		return
	}
	if v.(FutureUpdate) != *fut {
		return
	}
	makeUpdate(ctx, fut)
}

// handleErrorAndRequeue logs the failure and pushes the future update back
// onto the LOW-priority reindex queue. Retries are deliberately deprioritized
// so a flaky user contract can't starve fresh tx events. If a new fresh tx
// arrives for the same user while the retry sits in reindexQueue, the fresh
// producer will Store-overwrite the map entry, the queued retry will be
// detected as a ghost by processOrSkip, and the fresh entry will run instead.
func handleErrorAndRequeue(ctx context.Context, fut *FutureUpdate, reason string, err error) {
	if err != nil {
		fmt.Printf("%s: %v\n", reason, err)
	} else {
		fmt.Println(reason)
	}

	if fut == nil {
		return
	}

	select {
	case reindexQueue <- *fut:
	case <-ctx.Done():
	case <-Shutdown:
	}
}

func makeUpdate(ctx context.Context, fut *FutureUpdate) {
	if fut.TxUtime > time.Now().Unix()-updateDelayBufferSeconds {
		select {
		case <-time.After(sleepTime * time.Second):
		case <-ctx.Done():
			return
		case <-Shutdown:
			return
		}
	}

	key := mapKeyOf(fut)

	if fut.ContractAddress == "" {
		_, service := resolveSDKConfig(fut.Pool.Name)
		if service == nil {
			updateMap.Delete(key)
			fmt.Printf("makeUpdate: unknown pool %s\n", fut.Pool.Name)
			return
		}
		addr, _ := service.CalculateUserSCAddress(address.MustParseAddr(fut.Address))
		fut.ContractAddress = addr.String()
	}

	dataBocBase64, err := GetAccountStateToncenter(ctx, fut.ContractAddress)
	if err != nil {
		// Keep dedup; requeue.
		handleErrorAndRequeue(ctx, fut, fmt.Sprintf("toncenter state %s", fut.ContractAddress), err)
		return
	}
	if dataBocBase64 == "" {
		// Empty state means the user contract is uninit. Drop dedup so future
		// txs can re-enqueue it.
		updateMap.Delete(key)
		fmt.Printf("toncenter empty state for %s (pool %s)\n", fut.ContractAddress, fut.Pool.Name)
		return
	}

	ts := fut.TxUtime
	if fut.CreatedAt > fut.TxUtime+updateDelayBufferSeconds {
		ts = fut.CreatedAt
	}

	changed, err := applyUserStateBoc(fut.Pool, fut.Address, fut.ContractAddress, fut.SubaccountID, dataBocBase64, ts)
	if err != nil {
		handleErrorAndRequeue(ctx, fut, fmt.Sprintf("apply user state %s", fut.ContractAddress), err)
		return
	}

	updateMap.Delete(key)
	fmt.Printf("user updated wallet=%s pool=%s sub=%d contract=%s ts=%s changed=%v recheck=%v\n",
		fut.Address, fut.Pool.Name, fut.SubaccountID, fut.ContractAddress,
		time.Unix(ts, 0).Format(time.RFC3339), changed, fut.IsRecheck)

	// If a fresh tx didn't visibly change anything in DB, TonCenter probably
	// hadn't indexed the new state yet — schedule one delayed retry. The
	// IsRecheck guard caps this at exactly one extra attempt; if it still
	// shows nothing changed, we let the next 2-hour reindex pick it up.
	if !changed && !fut.IsRecheck {
		scheduleRecheck(ctx, fut)
	}
}

// scheduleRecheck waits recheckDelay and re-enqueues the same FutureUpdate
// with IsRecheck=true onto the fresh queue. If, while we were sleeping, a
// newer fresh tx for the same user arrived (i.e. updateMap already has an
// entry for the key), we skip — that fresh path will fetch the latest state
// itself, our recheck is redundant.
func scheduleRecheck(ctx context.Context, src *FutureUpdate) {
	recheck := *src
	recheck.IsRecheck = true
	recheck.CreatedAt = time.Now().Unix()

	go func() {
		select {
		case <-time.After(recheckDelay):
		case <-ctx.Done():
			return
		case <-Shutdown:
			return
		}

		key := mapKeyOf(&recheck)
		if _, loaded := updateMap.LoadOrStore(key, recheck); loaded {
			return
		}
		select {
		case freshQueue <- recheck:
		case <-ctx.Done():
			updateMap.Delete(key)
		case <-Shutdown:
			updateMap.Delete(key)
		}
	}()
}

// getPoolByName returns pool config by name.
func getPoolByName(name string) (config.Pool, bool) {
	for _, p := range config.Pools {
		if p.Name == name {
			return p, true
		}
	}
	return config.Pool{}, false
}

// startReindexScheduler runs a periodic job which, on startup and every
// reindexInterval, iterates through all users in the DB and gradually
// enqueues them for refresh via makeUpdate.
func startReindexScheduler(ctx context.Context) {
	go enqueueAllUsersGradually(ctx)

	ticker := time.NewTicker(reindexInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-Shutdown:
			return
		case <-ticker.C:
			enqueueAllUsersGradually(ctx)
		}
	}
}

func enqueueAllUsersGradually(ctx context.Context) {
	db, _ := config.GetDBInstance()

	type orderedUser struct {
		WalletAddress   string    `gorm:"column:wallet_address"`
		Pool            string    `gorm:"column:pool"`
		SubaccountID    int16     `gorm:"column:subaccount_id"`
		ContractAddress string    `gorm:"column:contract_address"`
		UpdatedAt       time.Time `gorm:"column:updated_at"`
		LastUtime       int64     `gorm:"column:last_utime"`
	}

	usersTable := config.GetTableName(db, &config.OnchainUser{})
	logsTable := config.GetTableName(db, &config.OnchainLog{})
	query := fmt.Sprintf(`
SELECT u.wallet_address, u.pool, u.subaccount_id, u.contract_address, u.updated_at,
       COALESCE(MAX(l.utime), 0) AS last_utime
FROM %s u
LEFT JOIN %s l
  ON l.user_address = u.wallet_address AND l.pool = u.pool AND l.subaccount_id = u.subaccount_id
GROUP BY u.wallet_address, u.pool, u.subaccount_id, u.contract_address, u.updated_at
ORDER BY (COUNT(l.hash) = 0) DESC, MAX(l.utime) DESC, u.wallet_address ASC
LIMIT ? OFFSET ?`, usersTable, logsTable)

	batchSize := 500
	for offset := 0; ; offset += batchSize {
		select {
		case <-ctx.Done():
			return
		case <-Shutdown:
			return
		default:
		}

		var rows []orderedUser
		if err := db.Raw(query, batchSize, offset).Scan(&rows).Error; err != nil {
			fmt.Printf("scheduler query error: %v\n", err)
			return
		}
		if len(rows) == 0 {
			return
		}
		for _, u := range rows {
			select {
			case <-ctx.Done():
				return
			case <-Shutdown:
				return
			default:
			}

			pool, ok := getPoolByName(u.Pool)
			if !ok {
				continue
			}
			fut := FutureUpdate{
				Address:         u.WalletAddress,
				ContractAddress: u.ContractAddress,
				SubaccountID:    u.SubaccountID,
				CreatedAt:       time.Now().Unix(),
				Pool:            pool,
				TxUtime:         u.UpdatedAt.Unix(),
			}
			// Reindex defers to fresh: only enqueue if no entry exists; if a
			// fresh tx arrives later it will Store-overwrite and our queue
			// item becomes a ghost.
			if _, loaded := updateMap.LoadOrStore(mapKeyOf(&fut), fut); loaded {
				continue
			}
			select {
			case reindexQueue <- fut:
				if u.LastUtime == 0 {
					fmt.Printf("enqueue user wallet=%s pool=%s sub=%d priority=no_tx\n", u.WalletAddress, pool.Name, u.SubaccountID)
				} else {
					fmt.Printf("enqueue user wallet=%s pool=%s sub=%d priority=recent last=%s\n",
						u.WalletAddress, pool.Name, u.SubaccountID,
						time.Unix(u.LastUtime, 0).Format(time.RFC3339))
				}
			case <-ctx.Done():
				return
			case <-Shutdown:
				return
			}

			select {
			case <-time.After(reindexEnqueueDelay):
			case <-ctx.Done():
				return
			case <-Shutdown:
				return
			}
		}
	}
}

// ReindexAllUsersFromTonCenter is a one-shot full sweep of every known user.
// Intended for cmd/reindexer where it owns the worker pool from start to end.
func ReindexAllUsersFromTonCenter(ctx context.Context, cfg config.Config) {
	db, _ := config.GetDBInstance()

	workers := cfg.UserSyncWorkers
	if workers <= 0 {
		workers = 1
	}

	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go worker(ctx, &wg)
	}

	type minimalUser struct {
		WalletAddress   string `gorm:"column:wallet_address"`
		Pool            string `gorm:"column:pool"`
		SubaccountID    int16  `gorm:"column:subaccount_id"`
		ContractAddress string `gorm:"column:contract_address"`
	}

	const batchSize = 500
	enqueued := 0
enqueueLoop:
	for offset := 0; ; offset += batchSize {
		select {
		case <-ctx.Done():
			break enqueueLoop
		default:
		}

		var rows []minimalUser
		if err := db.Model(&config.OnchainUser{}).
			Select("wallet_address", "pool", "subaccount_id", "contract_address").
			Order("wallet_address asc").
			Limit(batchSize).Offset(offset).
			Find(&rows).Error; err != nil {
			fmt.Printf("reindex: load users: %v\n", err)
			break enqueueLoop
		}
		if len(rows) == 0 {
			break enqueueLoop
		}
		for _, u := range rows {
			pool, ok := getPoolByName(u.Pool)
			if !ok {
				continue
			}
			fut := FutureUpdate{
				Address:         u.WalletAddress,
				ContractAddress: u.ContractAddress,
				SubaccountID:    u.SubaccountID,
				CreatedAt:       time.Now().Unix(),
				Pool:            pool,
				// One-shot reindex: TxUtime far in the past so makeUpdate's
				// finalization wait is skipped (we know nothing fresh just
				// landed for these contracts).
				TxUtime: 0,
			}
			if _, loaded := updateMap.LoadOrStore(mapKeyOf(&fut), fut); loaded {
				continue
			}
			select {
			case reindexQueue <- fut:
				enqueued++
			case <-ctx.Done():
				break enqueueLoop
			}
		}
	}

	fmt.Printf("reindex: enqueued %d users; waiting for queue to drain\n", enqueued)

	// Drain: wait until both queues and the dedup map are empty (or ctx cancels).
drainLoop:
	for {
		mapEmpty := true
		updateMap.Range(func(_, _ interface{}) bool { mapEmpty = false; return false })
		if mapEmpty && len(freshQueue) == 0 && len(reindexQueue) == 0 {
			break drainLoop
		}
		select {
		case <-ctx.Done():
			break drainLoop
		case <-time.After(500 * time.Millisecond):
		}
	}

	closeShutdownOnce()
	wg.Wait()
	fmt.Println("reindex: done")
}

var shutdownClosed = false
var shutdownClosedMu sync.Mutex

func closeShutdownOnce() {
	shutdownClosedMu.Lock()
	defer shutdownClosedMu.Unlock()
	if shutdownClosed {
		return
	}
	close(Shutdown)
	shutdownClosed = true
}

