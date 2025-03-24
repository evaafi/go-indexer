Dton-based EVAA go-indexer
```Bash
docker build -t go-indexer .
```

```Bash
docker run -d \
  --name go-indexer \
  --restart unless-stopped \
  go-indexer
```
