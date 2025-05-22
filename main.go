package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/evaafi/go-indexer/config"
	"github.com/evaafi/go-indexer/indexer"
)

func main() {
	cfg, err := config.LoadConfig("config.yaml")
	config.CFG = cfg

	if err != nil {
		panic(fmt.Sprintf("Cant connect to database: %v", err))
	}

	db, err := config.GetDBInstance()
	if err != nil {
		panic(fmt.Sprintf("Cant create database istance: %v", err))
	}

	tables := []interface{}{
		&config.OnchainUser{},
		&config.OnchainLog{},
		&config.IndexerSyncState{},
		&config.EthenaAsCollateralAddressHistory{},
	}

	if cfg.MigrateOnStart {
		for _, table := range tables {
			if err := db.AutoMigrate(table); err != nil {
				panic(fmt.Sprintf("Migration error: %v", err))
			}
		}
	}

	if cfg.ForceResyncOnEveryStart {
		fmt.Println("Force resync enabled, truncating all indexing tables...")
		for _, table := range tables {
			if err := db.Exec(fmt.Sprintf("TRUNCATE TABLE %s RESTART IDENTITY CASCADE;", config.GetTableName(db, table))).Error; err != nil {
				panic(fmt.Sprintf("Failed to truncate table: %v", err))
			}
		}
		fmt.Println("All tables truncated successfully.")
	}

	config.EnsureInitialIdxSyncStateData(db)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	fmt.Println("Start indexing...")
	go indexer.RunIndexer(ctx, cfg)

	if cfg.Mode == config.ModeLiquidator {

	}

	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
	<-sigs
	log.Println("Received termination signal, stopping application...")

	close(indexer.Shutdown)

	indexer.WG.Wait()
	fmt.Println("Saving queue")
	err = indexer.SaveQueue()
	if err != nil {
		fmt.Printf("Error per saving queue: %s\n", err)
	}
	cancel()

	time.Sleep(3 * time.Second)
}
