# EVAA Indexer

A Go-based indexer for EVAA.finance. This service creates and maintains database tables with indexed blockchain data.

## Overview

The indexer performs the following tasks:

1. Connects to a specified PostgreSQL database
2. Creates required tables for storing indexed data:
   - Main pool/lp pool/alts pool users
   - Operation logs
3. Indexes blockchain data using [DTON](https://dton.io/) GraphQL API
4. Continuously updates indexed data to stay in sync with blockchain

## Features

- Automatic table creation and schema migration
- Parallel data indexing with configurable workers
- Force resync option for full data reindexation
- Continuous sync with blockchain state
- Robust error handling and automatic recovery

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
graphqlEndpoint: "https://dton.io/{your_api_key}/graphql"
userSyncWorkers: 32 depends on your dton plan
forceResyncOnEveryStart: false
```