package indexer

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"reflect"
	"strings"

	//"os"
	"sync"
	"time"

	"os"

	sdkConfig "github.com/evaafi/evaa-go-sdk/config"
	sdkPrincipal "github.com/evaafi/evaa-go-sdk/principal"
	"github.com/evaafi/go-indexer/config"
	"github.com/xssnick/tonutils-go/address"
	"github.com/xssnick/tonutils-go/tvm/cell"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type FutureUpdate struct {
	Address   string
	CreatedAt int64
	Pool      config.Pool
	TxUtime   int64
}

type MapKey struct {
	Address  string
	PoolName string
}

var (
	updateMap   sync.Map
	updateQueue               = make(chan FutureUpdate, 30000)
	sleepTime   time.Duration = 30
	Shutdown                  = make(chan struct{})
	WG          sync.WaitGroup
)

const queueFile = "update_queue.json"

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

	err = os.WriteFile(queueFile, data, 0644)
	if err != nil {
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

	err = json.Unmarshal(data, &queueData)
	if err != nil {
		return fmt.Errorf("error decoding JSON: %w", err)
	}

	for _, fut := range queueData {
		updateMap.Store(MapKey{Address: fut.Address, PoolName: fut.Pool.Name}, fut)
		updateQueue <- fut
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
		go worker(updateQueue, &WG)
	}

	for _, pool := range config.Pools {
		fmt.Printf("starting %s indexer \n", pool.Name)
		go corutineIndexer(ctx, cfg, pool)
	}

	// periodic full reindex: immediately on start and every 24 hours
	go func() {
		ReindexAllUsersFromTonCenter(ctx, cfg)
		ticker := time.NewTicker(24 * time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				ReindexAllUsersFromTonCenter(ctx, cfg)
			}
		}
	}()
}

// applyUserStateBoc decodes base64 BOC and updates the onchain user for given wallet and pool.
func applyUserStateBoc(pool config.Pool, walletAddress string, contractAddress string, dataBocBase64 string, txUtime int64) error {
	db, _ := config.GetDBInstance()

	decoded, err := base64.StdEncoding.DecodeString(dataBocBase64)
	if err != nil {
		return fmt.Errorf("failed to decode base64 user state: %w", err)
	}
	dataCell, err := cell.FromBOC(decoded)
	if err != nil {
		return fmt.Errorf("failed to create boc from base64 user state: %w", err)
	}

	var (
		service       *sdkPrincipal.Service
		sdkPoolConfig *sdkConfig.Config
	)
	if pool.Name == "main" {
		sdkPoolConfig = sdkConfig.GetMainMainnetConfig()
		service = sdkPrincipal.NewService(sdkPoolConfig)
	} else if pool.Name == "lp" {
		sdkPoolConfig = sdkConfig.GetLpMainnetConfig()
		service = sdkPrincipal.NewService(sdkPoolConfig)
	} else if pool.Name == "alts" {
		sdkPoolConfig = sdkConfig.GetAltsMainnetConfig()
		service = sdkPrincipal.NewService(sdkPoolConfig)
	} else if pool.Name == "stable" {
		sdkPoolConfig = sdkConfig.GetStableMainnetConfig()
		service = sdkPrincipal.NewService(sdkPoolConfig)
	}

	// calculate contract if not provided
	if contractAddress == "" {
		userContractAddress, _ := service.CalculateUserSCAddress(address.MustParseAddr(walletAddress))
		contractAddress = userContractAddress.String()
	}

	userSC := sdkPrincipal.NewUserSC(address.MustParseAddr(contractAddress))
	_, _ = userSC.SetAccData(dataCell)

	onchainUser := config.OnchainUser{}
	userPrincipals := userSC.Principals()
	onchainUser.Pool = pool.Name
	onchainUser.CodeVersion = int(userSC.CodeVersion())
	onchainUser.ContractAddress = contractAddress
	onchainUser.State = config.BigInt{Int: big.NewInt(userSC.UserState())}
	onchainUser.UpdatedAt = time.Unix(txUtime, 0)
	onchainUser.CreatedAt = time.Unix(txUtime, 0)
	onchainUser.WalletAddress = walletAddress

	principalsByID := make(map[string]*big.Int)
	for name, raw := range userPrincipals {
		if raw == nil {
			raw = big.NewInt(0)
		}
		principalsByID[name] = raw
	}
	normalizedPrincipals := make(config.Principals)
	for _, asset := range sdkPoolConfig.Assets {
		idStr := asset.ID.String()
		value := big.NewInt(0)
		if v, ok := principalsByID[idStr]; ok && v != nil {
			value = new(big.Int).Set(v)
		}
		key := config.BigInt{Int: new(big.Int).Set(asset.ID)}
		normalizedPrincipals[key] = config.BigInt{Int: value}
	}
	onchainUser.Principals = normalizedPrincipals

	if err := insertOrUpdate(db, onchainUser); err != nil {
		return err
	}
	return nil
}

// ReindexAllUsersFromTonCenter refreshes all known user states by fetching account states from TON Center.
func ReindexAllUsersFromTonCenter(ctx context.Context, cfg config.Config) {
	db, _ := config.GetDBInstance()

	type minimalUser struct {
		WalletAddress   string
		Pool            string
		ContractAddress string
	}

	var rows []minimalUser
	if err := db.Model(&config.OnchainUser{}).
		Select("wallet_address", "pool", "contract_address").
		Find(&rows).Error; err != nil {
		fmt.Printf("reindex: failed to load users: %v\n", err)
		return
	}

	if len(rows) == 0 {
		fmt.Println("reindex: no users to refresh")
		return
	}

	// group by pool and build contract->wallet for O(1) lookup
	poolToContracts := make(map[string][]string)
	contractToWallet := make(map[string]map[string]string) // pool -> contract -> wallet
	for _, u := range rows {
		if contractToWallet[u.Pool] == nil {
			contractToWallet[u.Pool] = make(map[string]string)
		}
		// deduplicate contracts per pool
		if _, exists := contractToWallet[u.Pool][u.ContractAddress]; !exists {
			poolToContracts[u.Pool] = append(poolToContracts[u.Pool], u.ContractAddress)
		}
		contractToWallet[u.Pool][u.ContractAddress] = u.WalletAddress
	}

	// batch size for TON Center accountStates; conservative default
	batchSize := 100

	for _, pool := range config.Pools {
		select {
		case <-ctx.Done():
			return
		default:
		}

		contracts := poolToContracts[pool.Name]
		if len(contracts) == 0 {
			continue
		}

		total := len(contracts)
		success := 0
		failures := 0
		for i := 0; i < total; i += batchSize {
			select {
			case <-ctx.Done():
				return
			default:
			}

			end := i + batchSize
			if end > total {
				end = total
			}
			batch := contracts[i:end]

			resp, err := fetchTonCenterAccountStates(cfg.TonCenterAPIKey, batch)
			if err != nil {
				failures += len(batch)
				maxShow := 3
				if len(batch) < maxShow {
					maxShow = len(batch)
				}
				fmt.Printf("reindex: toncenter error pool=%s batch=%d-%d/%d size=%d sample=%v err=%v\n", pool.Name, i+1, end, total, len(batch), batch[:maxShow], err)
				continue
			}

			now := time.Now().Unix()

			// Only process addresses that were requested in this batch to avoid duplicates (raw vs friendly)
			for _, contractAddr := range batch {
				dataBoc := resp[contractAddr]
				if dataBoc == "" {
					// quietly skip empty state
					continue
				}
				wallet := contractToWallet[pool.Name][contractAddr]
				if wallet == "" {
					// unexpected, but skip quietly
					continue
				}
				if err := applyUserStateBoc(pool, wallet, contractAddr, dataBoc, now); err != nil {
					failures++
					bocPrefix := dataBoc
					if len(bocPrefix) > 16 {
						bocPrefix = bocPrefix[:16]
					}
					fmt.Printf("reindex: apply_state_err pool=%s addr=%s wallet=%s boc_len=%d boc_prefix=%s err=%v\n", pool.Name, contractAddr, wallet, len(dataBoc), bocPrefix, err)
					continue
				}
				success++
			}
			fmt.Printf("reindex: pool=%s progress %d/%d (ok=%d, fail=%d)\n", pool.Name, end, total, success, failures)
		}
		fmt.Printf("reindex: pool=%s finished (ok=%d, fail=%d)\n", pool.Name, success, failures)
	}
}

func corutineIndexer(ctx context.Context, cfg config.Config, pool config.Pool) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		wait, err := processIndex(cfg, pool)

		if err != nil {
			fmt.Println(err)
		}

		if wait {
			time.Sleep(10 * time.Second)
		}

		time.Sleep(1 * time.Second)
	}

}

func processIndex(cfg config.Config, pool config.Pool) (bool, error) {
	var db, _ = config.GetDBInstance()

	var state config.OnchainSyncState
	poolValue := pool.Name

	if err := db.Where("pool = ?", poolValue).First(&state).Error; err != nil {
		return false, fmt.Errorf("error per getting poolValue")
	}
	lastUtime := state.LastUtime
	//fmt.Printf("pool %s: current utime %d\n", lastUtime);
	pageSize := cfg.MaxPageSize
	transactions, lastUtime, err := ProcessTransactions(cfg.GraphQLEndpoint, pool.Address, lastUtime, pageSize)

	if err != nil {
		return false, fmt.Errorf("error per processing transactions %s %d", pool.Name, lastUtime)
	}

	if len(transactions) == 0 && time.Now().Unix() > lastUtime+config.UtimeAddendum {
		// advance and persist sync state when window is empty but time has moved forward
		lastUtime += config.UtimeAddendum
		state.LastUtime = lastUtime
		if err := db.Save(&state).Error; err != nil {
			return false, fmt.Errorf("error updating IdxSyncState: %w", err)
		}

		return true, nil
	}

	if len(transactions) == 0 {
		return true, nil
	}

	fmt.Printf("indexer %s got %d new transactions \n", pool.Name, len(transactions))

	state.LastUtime = min(lastUtime, state.LastUtime+config.UtimeAddendum)
	if err := db.Save(&state).Error; err != nil {
		return false, fmt.Errorf("error updating IdxSyncState: %w", err)
	}

	var logs []config.OnchainLog

	for _, tr := range transactions {
		// Skip already processed transactions by hash+pool to avoid re-queuing the same user repeatedly
		var existingCount int64
		if err := db.Model(&config.OnchainLog{}).Where("hash = ? AND pool = ?", tr.Hash, pool.Name).Count(&existingCount).Error; err == nil && existingCount > 0 {
			continue
		}
		logVersion := 1

		if pool.Name == "lp" && tr.LT < 49712577000001 || pool.Name == "main" && tr.LT < 49828980000001 {
			logVersion = 0
		}

		for _, body := range tr.OutMsgBodies {
			idxLog, err := ParseLogMessage(body, logVersion)

			idxLog.Pool = pool.Name
			idxLog.CreatedAt = time.Unix(idxLog.Utime, 0)
			idxLog.Hash = tr.Hash

			if err != nil {
				if strings.Contains(err.Error(), "unknown log type") {
					continue
				}
				fmt.Printf("cannot parse log message hash: %s %s \n", tr.Hash, err)
				continue
			}

			logs = append(logs, idxLog)

			key := MapKey{Address: idxLog.UserAddress, PoolName: pool.Name}

			if _, ok := updateMap.Load(key); ok {
				fmt.Printf("update skip duplicate %s (%s)\n", idxLog.UserAddress, pool.Name)
				continue
			}

			fut := FutureUpdate{
				Address:   idxLog.UserAddress,
				CreatedAt: time.Now().Unix(),
				Pool:      pool,
				TxUtime:   idxLog.Utime,
			}
			updateMap.Store(key, fut)
			updateQueue <- fut
			fmt.Printf("update queued %s (%s) tx_utime=%d\n", idxLog.UserAddress, pool.Name, idxLog.Utime)

		}
	}

	//fmt.Printf("%s pool start inserting\n", pool.Name)

	batchSize := 1000

	for i := 0; i < len(logs); i += batchSize {
		end := i + batchSize
		if end > len(logs) {
			end = len(logs)
		}

		batch := logs[i:end]

		if err := db.Clauses(clause.OnConflict{DoNothing: true}).Create(&batch).Error; err != nil {
			return false, fmt.Errorf("error inserting records: %w", err)
		}
	}

	fmt.Printf("%s pool inserted\n", pool.Name)

	return len(transactions) >= pageSize, nil
}

func insertOrUpdate[T config.UserInterface](db *gorm.DB, data T) error {
	/*result := db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "wallet_address"}},
		DoUpdates: clause.AssignmentColumns(getUpdatableFields[T](db)),
	}).Create(&data)

	return result.Error*/
	objType := reflect.TypeOf(data)
	if objType.Kind() != reflect.Struct {
		return fmt.Errorf("insertOrUpdate: T must be a struct, got %T", data)
	}

	updatableFields := getUpdatableFields[T](db)

	result := db.Model(&data).
		Where("wallet_address = ?", data.GetWalletAddress()).
		Select(updatableFields).
		Updates(data)

	if result.RowsAffected == 0 {
		return db.Create(&data).Error
	}

	return result.Error
}

func getUpdatableFields[T any](db *gorm.DB) []string {
	obj := new(T)
	objType := reflect.TypeOf(obj).Elem()

	if objType.Kind() != reflect.Struct {
		panic("getUpdatableFields: T must be a struct")
	}

	if objType.Kind() != reflect.Struct {
		panic(fmt.Sprintf("getUpdatableFields: %T is not a struct", obj))
	}

	fields := []string{}
	stmt := db.Model(obj).Statement
	stmt.Parse(obj)

	if stmt.Schema == nil {
		panic("getUpdatableFields: Schema is nil, check if model is registered in GORM")
	}

	for _, field := range stmt.Schema.Fields {
		if field.DBName != "created_at" {
			fields = append(fields, field.DBName)
		}
	}
	return fields
}

func worker(updateQueue <-chan FutureUpdate, wg *sync.WaitGroup) {
	defer wg.Done()
	for {
		select {
		case <-Shutdown:
			fmt.Println("Worker received shutdown signal, finishing current task...")
			return
		case fut := <-updateQueue:
			makeUpdate(&fut)
		}
	}
}

func handleErrorAndRequeue(fut *FutureUpdate, reason string, err error) {
	if err != nil {
		fmt.Printf("%s: %v\n", reason, err)
	} else {
		fmt.Println(reason)
	}

	if fut != nil {
		key := MapKey{Address: fut.Address, PoolName: fut.Pool.Name}

		updateMap.Store(key, fut)
		updateQueue <- *fut
	}
}

const updateDelayBufferSeconds int64 = 30

func makeUpdate(fut *FutureUpdate) {
	//update := fut.CreatedAt
	if fut.TxUtime > time.Now().Unix()-updateDelayBufferSeconds {
		time.Sleep(sleepTime * time.Second)
	}

	key := MapKey{Address: fut.Address, PoolName: fut.Pool.Name}
	defer updateMap.Delete(key)

	// no direct DB access here; persistence is handled in applyUserStateBoc

	var (
		service             *sdkPrincipal.Service
		userContractAddress *address.Address
		sdkPoolConfig       *sdkConfig.Config
	)

	if fut.Pool.Name == "main" {
		sdkPoolConfig = sdkConfig.GetMainMainnetConfig()
		service = sdkPrincipal.NewService(sdkPoolConfig)
	} else if fut.Pool.Name == "lp" {
		sdkPoolConfig = sdkConfig.GetLpMainnetConfig()
		service = sdkPrincipal.NewService(sdkPoolConfig)
	} else if fut.Pool.Name == "alts" {
		sdkPoolConfig = sdkConfig.GetAltsMainnetConfig()
		service = sdkPrincipal.NewService(sdkPoolConfig)
	} else if fut.Pool.Name == "stable" {
		sdkPoolConfig = sdkConfig.GetStableMainnetConfig()
		service = sdkPrincipal.NewService(sdkPoolConfig)
	}
	userContractAddress, _ = service.CalculateUserSCAddress(address.MustParseAddr(fut.Address))

	// Switch to TON Center: get account state BOC and apply
	resp, err := fetchTonCenterAccountStates(config.CFG.TonCenterAPIKey, []string{userContractAddress.String()})
	if err != nil {
		handleErrorAndRequeue(fut, "failed to get user state from toncenter", err)
		return
	}
	dataBocBase64 := resp[userContractAddress.String()]
	if dataBocBase64 == "" {
		for _, v := range resp {
			dataBocBase64 = v
			break
		}
	}
	if dataBocBase64 == "" {
		handleErrorAndRequeue(fut, fmt.Sprintf("toncenter returned empty state for %s", userContractAddress.String()), nil)
		return
	}

	ts := fut.TxUtime
	if fut.CreatedAt > (fut.TxUtime + updateDelayBufferSeconds) {
		ts = fut.CreatedAt
	}
	if err := applyUserStateBoc(fut.Pool, fut.Address, userContractAddress.String(), dataBocBase64, ts); err != nil {
		handleErrorAndRequeue(fut, "failed to apply user state", err)
		return
	}
	fmt.Printf("user %s updated (pool %s)\n", userContractAddress.String(), fut.Pool.Name)
}
