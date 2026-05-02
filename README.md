# EVAA Indexer

A Go-based indexer for EVAA.finance. This service creates and maintains database tables with indexed blockchain data.

## Overview

The indexer performs the following tasks:

1. Connects to a specified PostgreSQL database
2. Creates required tables for storing indexed data:
   - Main pool/lp pool/alts pool/stable pool users
   - Operation logs
3. Indexes blockchain data via the [TonCenter v3](https://toncenter.com/api/v3/) HTTP API:
   - `/api/v3/messages` for pool external-out logs (per-pool transaction stream)
   - `/api/v3/accountStates` for user smart-contract storage data (state refresh)
4. Continuously updates indexed data to stay in sync with blockchain

## Features

- Automatic table creation and schema migration
- Parallel data indexing with configurable workers
- Force resync option for full data reindexation
- Continuous sync with blockchain state
- Shared TonCenter token-bucket rate limiter (`golang.org/x/time/rate`) across both endpoints
- Retries with exponential backoff + jitter and `Retry-After` honoring on 429/5xx
- Context-aware cancellation throughout (workers, fetchers, schedulers)
- Graceful shutdown saves the in-flight queue to disk and resumes on next start

## Quick Start

```bash
docker build -t go-indexer .
```

```bash
docker run -d \
  --name go-indexer \
  --restart unless-stopped \
  go-indexer
```

## Configuration

Configure database connection and indexing parameters in `config.yaml`:

```yaml
mode: "indexer"
dbType: "postgres"
dbHost: "ip"
dbPort: 5432
dbUser: "your_user"
dbPass: "your_password"
dbName: "your_database"
userSyncWorkers: 8       # parallel TonCenter state-fetch workers
forceResyncOnEveryStart: false
migrateOnStart: false
maxPageSize: 100         # TonCenter messages page size (max 1000)

# TonCenter rate limit shared by both /accountStates and /messages.
# With an API key TonCenter usually allows ~10 rps; size burst >= rps.
toncenterApiKey: "your_toncenter_api_key"
toncenterRPS: 5
toncenterBurst: 10
```

## One-shot full reindex

To refresh every known user's on-chain state in one pass (for example after a
schema change or to recover from a long outage), run the bundled `reindexer`
binary against the same `config.yaml`:

```bash
go run ./cmd/reindexer
```

It enqueues every user from the DB, drains via the worker pool, then exits.

## Tests

Unit-style tests run by default:

```bash
go test ./...
```

Live-network tests against TonCenter live behind the `integration` build tag
and require a valid `toncenterApiKey` in `config.yaml`:

```bash
go test -tags=integration ./indexer/...
```
