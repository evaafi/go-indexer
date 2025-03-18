package config

import (
	"database/sql/driver"
	"errors"
	"fmt"
	"log"
	"math/big"
	"os"
	"sync"
	"time"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

var (
	DBInstance *gorm.DB
	dbOnce     sync.Once
	CFG        Config
)

func GetDBInstance() (*gorm.DB, error) {
	var err error
	dbOnce.Do(func() {
		var dsn string
		if CFG.DBPass != "" {
			dsn = fmt.Sprintf("host=%s port=%d user=%s password=%s dbname=%s sslmode=prefer",
				CFG.DBHost, CFG.DBPort, CFG.DBUser, CFG.DBPass, CFG.DBName)
		} else {
			dsn = fmt.Sprintf("host=%s port=%d user=%s dbname=%s sslmode=prefer",
				CFG.DBHost, CFG.DBPort, CFG.DBUser, CFG.DBName)
		}

		DBInstance, err = gorm.Open(postgres.Open(dsn), &gorm.Config{
			Logger: logger.New(
				log.New(os.Stdout, "\r\n", log.LstdFlags),
				logger.Config{
					SlowThreshold: 0,
					LogLevel:      logger.Warn,
					Colorful:      true,
				},
			),
		})
	})
	return DBInstance, err
}

func GetTableName(db *gorm.DB, model interface{}) string {
	stmt := &gorm.Statement{DB: db}
	stmt.Parse(model)
	return stmt.Schema.Table
}

type UserInterface interface {
    GetWalletAddress() string
}

func (u *UserFields) GetWalletAddress() string {
    return u.WalletAddress
}


func (u IdxUsers) GetWalletAddress() string {
    return u.WalletAddress
}

func (u IdxUsersLp) GetWalletAddress() string {
    return u.WalletAddress
}

func (u IdxUsersAlts) GetWalletAddress() string {
    return u.WalletAddress
}

type UserFields struct {
	WalletAddress   string    `gorm:"primaryKey;column:wallet_address"`
	ContractAddress string    `gorm:"unique;column:contract_address;not null"`
	CodeVersion     int       `gorm:"column:code_version;not null"`
	CreatedAt       time.Time `gorm:"column:created_at;not null"`
	UpdatedAt       time.Time `gorm:"column:updated_at;not null"`
	State           BigInt    `gorm:"column:state;not null;type:NUMERIC"`
}

type IdxUsers struct {
	UserFields
	TonPrincipal   BigInt `gorm:"column:ton_principal;type:NUMERIC"`
	JusdtPrincipal BigInt `gorm:"column:jusdt_principal;type:NUMERIC"`
	JusdcPrincipal BigInt `gorm:"column:jusdc_principal;type:NUMERIC"`
	StTonPrincipal BigInt `gorm:"column:stton_principal;type:NUMERIC"`
	TsTonPrincipal BigInt `gorm:"column:tston_principal;type:NUMERIC"`
	UsdtPrincipal  BigInt `gorm:"column:usdt_principal;type:NUMERIC"`
}

type IdxUsersAlts struct {
	UserFields
	TonPrincipal  BigInt `gorm:"column:ton_principal;not null;default:0;type:NUMERIC"`
	UsdtPrincipal BigInt `gorm:"column:usdt_principal;not null;default:0;type:NUMERIC"`
	CatiPrincipal BigInt `gorm:"column:cati_principal;not null;default:0;type:NUMERIC"`
	NotPrincipal  BigInt `gorm:"column:not_principal;not null;default:0;type:NUMERIC"`
	DogsPrincipal BigInt `gorm:"column:dogs_principal;not null;default:0;type:NUMERIC"`
}

type IdxUsersLp struct {
	UserFields
	TonPrincipal           BigInt `gorm:"column:ton_principal;not null;default:0;type:NUMERIC"`
	UsdtPrincipal          BigInt `gorm:"column:usdt_principal;not null;default:0;type:NUMERIC"`
	TonUsdtDedustPrincipal BigInt `gorm:"column:tonusdt_dedust_principal;not null;default:0;type:NUMERIC"`
	TonStormPrincipal      BigInt `gorm:"column:ton_storm_principal;not null;default:0;type:NUMERIC"`
	UsdtStormPrincipal     BigInt `gorm:"column:usdt_storm_principal;not null;default:0;type:NUMERIC"`
}

type BigInt struct {
	*big.Int
}

func (b BigInt) Value() (driver.Value, error) {
	if b.Int == nil {
		return "0", nil
	}
	return b.String(), nil
}

func (b *BigInt) Scan(value interface{}) error {
	if value == nil {
		b.Int = big.NewInt(0)
		return nil
	}
	switch v := value.(type) {
	case []byte:
		s := string(v)
		i, ok := new(big.Int).SetString(s, 10)
		if !ok {
			return fmt.Errorf("cannot convert %s to big.Int", s)
		}
		b.Int = i
	case string:
		i, ok := new(big.Int).SetString(v, 10)
		if !ok {
			return fmt.Errorf("cannot convert %s to big.Int", v)
		}
		b.Int = i
	default:
		return fmt.Errorf("unsupported type: %T", value)
	}
	return nil
}

type IdxLog struct {
	Hash                              string    `gorm:"primaryKey;column:hash;type:string"`
	Pool                              string    `gorm:"primaryKey;column:pool"`
	Utime                             int64     `gorm:"column:utime;not null"`
	TxType                            string    `gorm:"column:tx_type;not null"`
	TxSubType						  string    `gorm:"column:tx_sub_type;"`
	SenderAddress                     string    `gorm:"column:sender_address;not null"`
	UserAddress                       string    `gorm:"column:user_address;not null"`
	AttachedAssetAddress              BigInt    `gorm:"column:attached_asset_address;type:NUMERIC"`
	AttachedAssetAmount               BigInt    `gorm:"column:attached_asset_amount;type:NUMERIC"`
	AttachedAssetPrincipal            BigInt    `gorm:"column:attached_asset_principal;type:NUMERIC"`
	AttachedAssetTotalSupplyPrincipal BigInt    `gorm:"column:attached_asset_total_supply_principal;type:NUMERIC"`
	AttachedAssetTotalBorrowPrincipal BigInt    `gorm:"column:attached_asset_total_borrow_principal;type:NUMERIC"`
	AttachedAssetSRate                BigInt    `gorm:"column:attached_asset_s_rate;type:NUMERIC"`
	AttachedAssetBRate                BigInt    `gorm:"column:attached_asset_b_rate;type:NUMERIC"`
	RedeemedAssetAddress              BigInt    `gorm:"column:redeemed_asset_address;type:NUMERIC"`
	RedeemedAssetAmount               BigInt    `gorm:"column:redeemed_asset_amount;type:NUMERIC"`
	RedeemedAssetPrincipal            BigInt    `gorm:"column:redeemed_asset_principal;type:NUMERIC"`
	RedeemedAssetTotalSupplyPrincipal BigInt    `gorm:"column:redeemed_asset_total_supply_principal;type:NUMERIC"`
	RedeemedAssetTotalBorrowPrincipal BigInt    `gorm:"column:redeemed_asset_total_borrow_principal;type:NUMERIC"`
	RedeemedAssetSRate                BigInt    `gorm:"column:redeemed_asset_s_rate;type:NUMERIC"`
	RedeemedAssetBRate                BigInt    `gorm:"column:redeemed_asset_b_rate;type:NUMERIC"`
	CreatedAt                         time.Time `gorm:"column:created_at;default:now()"`
}

type IdxSyncState struct {
	Pool   string 	`gorm:"primaryKey;column:pool"`
	LastLt int64 	`gorm:"column:last_lt"`
	LastUtime int64 `gorm:"column:last_utime"`
}

func EnsureInitialIdxSyncStateData(db *gorm.DB) {
	initialData := []IdxSyncState{
		{Pool: "main", LastLt: 0, LastUtime: 1714879105},
		{Pool: "alts", LastLt: 0, LastUtime: 1732117342},
		{Pool: "lp", LastLt: 0, LastUtime: 1725205342},
	}

	for _, data := range initialData {
		var existing IdxSyncState

		err := db.First(&existing, "pool = ?", data.Pool).Error

		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				if err := db.Create(&data).Error; err != nil {
					fmt.Printf("Failed to insert initial data for pool %s: %v\n", data.Pool, err)
				} else {
					fmt.Printf("Inserted initial data for pool %s\n", data.Pool)
				}
			} else {
				fmt.Printf("Error checking existing record: %v\n", err)
			}
		}
	}
}
