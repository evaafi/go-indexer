FROM golang:1.24-alpine

WORKDIR /app

RUN apk add --no-cache \
    git \
    ca-certificates

COPY go.mod go.sum ./

RUN go mod download

COPY . .

RUN go build -o /app/go-indexer

CMD ["/app/go-indexer"]
