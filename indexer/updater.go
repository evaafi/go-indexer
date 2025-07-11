package indexer
/*
import (
	"context"
	"log"
	"time"

	"github.com/evaafi/evaa-go-sdk/asset"
	sdkConfig "github.com/evaafi/evaa-go-sdk/config"
	"github.com/evaafi/evaa-go-sdk/price"
	"github.com/evaafi/go-indexer/config"
	"github.com/xssnick/tonutils-go/liteclient"
	"github.com/xssnick/tonutils-go/ton"
)

var (
	PoolParsers = make(map[string]*asset.Parser)
	PoolPrices  = make(map[string]*price.Prices)
	poolConfigs = []struct {
		Name   string
		Config *sdkConfig.Config
	}{
		{Name: config.PoolMain.Name, Config: sdkConfig.GetMainMainnetConfig()},
			{Name: config.PoolLp.Name, Config: sdkConfig.GetLpMainnetConfig()},
			{Name: config.PoolAlts.Name, Config: sdkConfig.GetAltsMainnetConfig()},
	}
)

func init() {
	go RunUpdatePoolsConfigPeriodically()
	go StartUpdatePrices()
}

func StartUpdatePrices() {
	go func() {
		ctx := context.Background()
		ticker := time.NewTicker(1 * time.Minute)
		defer ticker.Stop()

		for {
			for _, config := range poolConfigs {
				svc := price.NewService(config.Config, nil)

				p, err := svc.GetPrices(ctx, "https://api.evaa.space/api/prices")
				if err != nil {
					log.Printf("price update error: %v", err)
				} else {
					PoolPrices[config.Name] = p
				}
			}
			<-ticker.C
		}
	}()
}

func RunUpdatePoolsConfigPeriodically() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()

	updatePoolsConfig()

	for range ticker.C {
		updatePoolsConfig()
	}
}

func updatePoolsConfig() {
	client := liteclient.NewConnectionPool()
	// connect to mainnet lite servers
	err := client.AddConnectionsFromConfigUrl(context.Background(), "https://ton-blockchain.github.io/global.config.json")
	if err != nil {
		log.Fatalln("connection err: ", err.Error())
		return
	}

	for _, config := range poolConfigs {
		log.Printf("updating assets of %s pool", config.Name)
		api := ton.NewAPIClient(client, ton.ProofCheckPolicyFast).WithRetry()
		parser := asset.NewParser(config.Config)

		addr := config.Config.MasterAddress

		block, err := api.CurrentMasterchainInfo(context.Background())
		if err != nil {
			log.Println("error per getting current block")
		}
		assetsData, err := api.WaitForBlock(block.SeqNo).RunGetMethod(context.Background(), block, addr, "getAssetsData")
		if err != nil {
			log.Println("error per getting getAssetsData")
		}
		assetsConfig, err := api.RunGetMethod(context.Background(), block, addr, "getAssetsConfig")
		if err != nil {
			log.Println("error per getting assetsConfig")
		}

		parser.SetInfo(assetsData.MustCell(0).AsDict(256), assetsConfig.MustCell(0).AsDict(256))
		PoolParsers[config.Name] = parser
	}
}
*/
