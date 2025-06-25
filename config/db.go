package config

import (
	"database/sql/driver"
	"encoding/json"
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

func (u OnchainUser) GetWalletAddress() string {
	return u.WalletAddress
}

type Principals map[BigInt]BigInt

func (p Principals) Value() (driver.Value, error) {
	return json.Marshal(p)
}

func (p *Principals) Scan(src interface{}) error {
	bytes, ok := src.([]byte)
	if !ok {
		return fmt.Errorf("unable to scan Principals, src is %T", src)
	}
	return json.Unmarshal(bytes, p)
}

func (b BigInt) MarshalJSON() ([]byte, error) {
	if b.Int == nil {
		return []byte("null"), nil
	}
	return []byte(`"` + b.String() + `"`), nil
}

func (b *BigInt) UnmarshalJSON(data []byte) error {
	if len(data) == 0 || string(data) == "null" {
		b.Int = big.NewInt(0)
		return nil
	}
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return err
	}
	bi, ok := new(big.Int).SetString(s, 10)
	if !ok {
		return fmt.Errorf("BigInt: cannot parse %q", s)
	}
	b.Int = bi
	return nil
}

type OnchainUser struct {
	WalletAddress   string     `gorm:"primaryKey;column:wallet_address"`
	Pool            string     `gorm:"primaryKey;column:pool"`
	ContractAddress string     `gorm:"primaryKey;unique;column:contract_address;not null"`
	CodeVersion     int        `gorm:"column:code_version;not null"`
	CreatedAt       time.Time  `gorm:"column:created_at;not null"`
	UpdatedAt       time.Time  `gorm:"column:updated_at;not null"`
	State           BigInt     `gorm:"column:state;not null;type:NUMERIC"`
	Principals      Principals `gorm:"column:principals;type:jsonb;not null;default:'{}'"`
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

type OnchainLog struct {
	Hash                              string    `gorm:"primaryKey;column:hash;type:string"`
	Pool                              string    `gorm:"primaryKey;column:pool"`
	Utime                             int64     `gorm:"column:utime;not null"`
	TxType                            string    `gorm:"column:tx_type;not null"`
	TxSubType                         string    `gorm:"column:tx_sub_type;"`
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

type IndexerSyncState struct {
	Pool      string `gorm:"primaryKey;column:pool"`
	LastLt    int64  `gorm:"column:last_lt"`
	LastUtime int64  `gorm:"column:last_utime"`
}

func EnsureInitialIdxSyncStateData(db *gorm.DB) {
	initialData := []IndexerSyncState{
		{Pool: "main", LastLt: 0, LastUtime: 1714879105},
		{Pool: "alts", LastLt: 0, LastUtime: 1732117342},
		{Pool: "lp", LastLt: 0, LastUtime: 1725205342},
	}

	for _, data := range initialData {
		var existing IndexerSyncState

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
