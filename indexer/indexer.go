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
		WG.Add(1)
		go worker(updateQueue, &WG)
	}

	for _, pool := range config.Pools {
		fmt.Printf("starting %s indexer \n", pool.Name)
		go corutineIndexer(ctx, cfg, pool)
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
		lastUtime += config.UtimeAddendum
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

	db, _ := config.GetDBInstance()

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

	rawState, err := GetRawState(config.CFG.GraphQLEndpoint, userContractAddress.String())

	if err != nil {
		handleErrorAndRequeue(fut, "failed to get user state", err)
		return
	}

	var userStateResponse GraphQLStatesResponse
	if err := json.Unmarshal([]byte(rawState), &userStateResponse); err != nil {
		handleErrorAndRequeue(fut, fmt.Sprintf("failed to unmarshal user state %s", rawState), err)
		return
	}

	if len(userStateResponse.Data.RawAccountStates) == 0 {
		handleErrorAndRequeue(fut, fmt.Sprintf("cannot get user state: %s %s %s %s, %s; adding again to queue", fut.Address, userContractAddress.String(), fut.Pool, rawState, userStateResponse.Data), nil)
		return
	}

	dataBoc, err := base64.StdEncoding.DecodeString(userStateResponse.Data.RawAccountStates[0].State)
	if err != nil {
		handleErrorAndRequeue(fut, fmt.Sprintf("failed to decode base64 user state: %s %s", userStateResponse.Data.RawAccountStates[0].State, userContractAddress.String()), err)
		return
	}

	data, err := cell.FromBOC(dataBoc)
	if err != nil {
		handleErrorAndRequeue(fut, fmt.Sprintf("failed to create boc from base64 user state: %s", userContractAddress.String()), err)
		return
	}

	user := sdkPrincipal.NewUserSC(userContractAddress)
	_, _ = user.SetAccData(data)

	onchainUser := config.OnchainUser{}

	userPrincipals := user.Principals()
	onchainUser.Pool = fut.Pool.Name
	onchainUser.CodeVersion = int(user.CodeVersion())
	onchainUser.ContractAddress = userContractAddress.String()
	onchainUser.State = config.BigInt{Int: big.NewInt(user.UserState())}
	if fut.CreatedAt > (fut.TxUtime + updateDelayBufferSeconds) {
		onchainUser.UpdatedAt = time.Unix(fut.CreatedAt, 0)
	} else {
		onchainUser.UpdatedAt = time.Unix(fut.TxUtime, 0)
	}
  	onchainUser.CreatedAt = time.Unix(fut.TxUtime, 0)
	onchainUser.WalletAddress = fut.Address

	principalMap := make(config.Principals)

	for name, raw := range userPrincipals {
		if raw == nil {
			raw = big.NewInt(0)
		}
		id := new(big.Int)
		id.SetString(name, 10)

		principalMap[config.BigInt{Int: id}] = config.BigInt{Int: new(big.Int).Set(raw)}
	}

	for _, asset := range sdkPoolConfig.Assets {
		key := config.BigInt{Int: new(big.Int).Set(asset.ID)}
		if _, ok := principalMap[key]; !ok {
			principalMap[key] = config.BigInt{Int: big.NewInt(0)}
		}
	}

	onchainUser.Principals = principalMap
	err = insertOrUpdate(db, onchainUser)

	if err != nil {
		fmt.Printf("error per insertOrUpdate  %s\n", err)
	}

	if fut.CreatedAt < (fut.TxUtime + updateDelayBufferSeconds) {
		fmt.Printf("user %s updated\n", userContractAddress.String())
	}

	onchainUser.Principals = principalMap
	if err := db.Save(&onchainUser).Error; err != nil {
		fmt.Printf("error per saving principals %s\n", err)
	}
}
