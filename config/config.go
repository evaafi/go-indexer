package config

import (
	/*"log"
	"math/big"*/
	"os"

	"gopkg.in/yaml.v2"
)

type Mode string

const (
	ModeIndexer    Mode = "indexer"
	ModeLiquidator Mode = "liquidator"
)

type DBType string

type Pool struct {
	Name    string
	Address string
}

/*func mustParseBigInt(s string) *big.Int {
	i, ok := new(big.Int).SetString(s, 10)
	if !ok {
		log.Fatalf("failed to parse big.Int from string: %s", s)
	}
	return i
}*/

var (
	UtimeAddendum int64 = 60 * 60 * 24 * 31
	PoolMain            = Pool{
		Name:    "main",
		Address: "EQC8rUZqR_pWV1BylWUlPNBzyiTYVoBEmQkMIQDZXICfnuRr",
	}
	PoolLp = Pool{
		Name:    "lp",
		Address: "EQBIlZX2URWkXCSg3QF2MJZU-wC5XkBoLww-hdWk2G37Jc6N",
	}
	PoolAlts = Pool{
		Name:    "alts",
		Address: "EQANURVS3fhBO9bivig34iyJQi97FhMbpivo1aUEAS2GYSu-",
	}
	PoolStable = Pool{
		Name:    "stable",
		Address: "EQCdIdXf1kA_2Hd9mbGzSFDEPA-Px-et8qTWHEXgRGo0K3zd",
	}
	Pools = []Pool{
		PoolMain,
		PoolLp,
		PoolAlts,
		PoolStable,
	}
	/*AssetMapping = map[string]*big.Int{
		"ton":             mustParseBigInt("11876925370864614464799087627157805050745321306404563164673853337929163193738"),
		"usdt":            mustParseBigInt("91621667903763073563570557639433445791506232618002614896981036659302854767224"),
		"stton":           mustParseBigInt("33171510858320790266247832496974106978700190498800858393089426423762035476944"),
		"tston":           mustParseBigInt("23103091784861387372100043848078515239542568751939923972799733728526040769767"),
		"tonusdt_dedust":  mustParseBigInt("101385043286520300676049067359330438448373069137841871026562097979079540439904"),
		"ton_storm":       mustParseBigInt("70772196878564564641575179045584595299167675028240038598329982312182743941170"),
		"usdt_storm":      mustParseBigInt("48839312865341050576546877995196761556581975995859696798601599030872576409489"),
		"not":             mustParseBigInt("63272935429475047547160566950018214503995518672462153218942708627846845749085"),
		"dogs":            mustParseBigInt("50918788872632134518291723145978712110022476979988675880017580610805163693009"),
		"cati":            mustParseBigInt("101563884026323503647891287974015286987607783840172791059852695820980647056177"),
	}*/
	TonAssetId           = "11876925370864614464799087627157805050745321306404563164673853337929163193738"
	UsdtAssetId          = "91621667903763073563570557639433445791506232618002614896981036659302854767224"
	JusdtAssetId         = "81203563022592193867903899252711112850180680126331353892172221352147647262515"
	JusdcAssetId         = "59636546167967198470134647008558085436004969028957957410318094280110082891718"
	StTonAssetId         = "33171510858320790266247832496974106978700190498800858393089426423762035476944"
	TsTonAssetId         = "23103091784861387372100043848078515239542568751939923972799733728526040769767"
	TonUsdtDedustAssetId = "101385043286520300676049067359330438448373069137841871026562097979079540439904"
	TonStormAssetId      = "70772196878564564641575179045584595299167675028240038598329982312182743941170"
	UsdtStormAssetId     = "48839312865341050576546877995196761556581975995859696798601599030872576409489"
	NotAssetId           = "63272935429475047547160566950018214503995518672462153218942708627846845749085"
	DogsAssetId          = "50918788872632134518291723145978712110022476979988675880017580610805163693009"
	CatiAssetId          = "101563884026323503647891287974015286987607783840172791059852695820980647056177"
	UsdeAssetId          = "98281638255104512379049519410242269170317135545117667048087651483812279009354"
	TsUsdeAssetId        = "33604868692898791249369426189145713090064546741393719833658701125733712580919"
)

const (
	DBPostgres DBType = "postgres"
	// DBRedis    DBType = "redis"
)

type Config struct {
	Mode                    Mode    `yaml:"mode"`
	DBType                  DBType  `yaml:"dbType"`
	DBHost                  string  `yaml:"dbHost"`
	DBPort                  int16   `yaml:"dbPort"`
	DBUser                  string  `yaml:"dbUser"`
	DBPass                  string  `yaml:"dbPass"`
	DBName                  string  `yaml:"dbName"`
	GraphQLEndpoint         string  `yaml:"graphqlEndpoint"`
	UserSyncWorkers         int     `yaml:"userSyncWorkers"`
	ForceResyncOnEveryStart bool    `yaml:"forceResyncOnEveryStart"`
	MigrateOnStart          bool    `yaml:"migrateOnStart"`
	MaxPageSize             int     `yaml:"maxPageSize"`
	TonCenterAPIKey         string  `yaml:"toncenterApiKey"`
	TonCenterRPS            float64 `yaml:"toncenterRPS"`
	TonCenterBurst          int     `yaml:"toncenterBurst"`
}

func LoadConfig(path string) (Config, error) {
	var cfg Config
	data, err := os.ReadFile(path)
	if err != nil {
		return cfg, err
	}
	err = yaml.Unmarshal(data, &cfg)
	return cfg, err
}
