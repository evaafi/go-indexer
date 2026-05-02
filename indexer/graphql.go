package indexer

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

// ProcessedTransaction is the canonical shape consumed by processIndex. It is
// produced from one external-out message: the message body carries the log,
// and the transaction it was emitted from gives us hash + LT.
type ProcessedTransaction struct {
	Hash         string
	LT           int64
	OutMsgBodies []string
}

// /api/v3/messages response (subset). For external-out messages, destination
// is null and out_msg_tx_hash points to the producing transaction.
type tcMessage struct {
	Hash           string  `json:"hash"`
	Source         string  `json:"source"`
	Destination    *string `json:"destination"`
	CreatedLT      string  `json:"created_lt"`
	CreatedAt      string  `json:"created_at"`
	OutMsgTxHash   string  `json:"out_msg_tx_hash"`
	MessageContent struct {
		Hash string `json:"hash"`
		Body string `json:"body"`
	} `json:"message_content"`
}

type tcMessagesResponse struct {
	Messages []tcMessage `json:"messages"`
	Error    string      `json:"error"`
}

// maxPagesPerCycle bounds how much catch-up work one ProcessTransactions call
// will do per pool per outer cycle. Without this, a fresh DB or a pool that
// went silent for months could pull millions of messages in a single tight
// loop and starve other pools of TonCenter rate budget.
const maxPagesPerCycle = 50

// ProcessTransactions fetches external-out messages emitted by `addr`,
// continuing from the previously stored cursor. Returns up to
// maxPagesPerCycle * pageSize messages plus the new (utime, lt) cursor.
//
// Pagination is by LT cursor (`start_lt = lastLt + 1`, `offset` always 0),
// which is monotonic and unique per source account, so we never hit
// TonCenter's offset cap and never silently drop messages even if a single
// second contains many txs. On the very first call after migration (or for a
// fresh pool) `initialLt == 0`; we then page once by `start_utime` to
// bootstrap, capture the first LT from the response, and continue with the LT
// cursor for the remaining pages of the cycle.
func ProcessTransactions(ctx context.Context, addr string, initialUtime, initialLt int64, pageSize int) ([]ProcessedTransaction, int64, int64, error) {
	if pageSize <= 0 {
		pageSize = 100
	}
	if pageSize > 1000 {
		pageSize = 1000
	}

	var (
		results      []ProcessedTransaction
		currentUtime = initialUtime
		currentLt    = initialLt
	)

	for page := 0; page < maxPagesPerCycle; page++ {
		select {
		case <-ctx.Done():
			return nil, currentUtime, currentLt, ctx.Err()
		default:
		}

		q := url.Values{}
		q.Add("source", addr)
		q.Set("destination", "null")
		q.Set("limit", strconv.Itoa(pageSize))
		q.Set("offset", "0")
		q.Set("sort", "asc")
		if currentLt > 0 {
			q.Set("start_lt", strconv.FormatInt(currentLt+1, 10))
		} else {
			q.Set("start_utime", strconv.FormatInt(currentUtime+1, 10))
		}

		var resp tcMessagesResponse
		if err := tcGet(ctx, "/api/v3/messages", q, &resp); err != nil {
			return nil, currentUtime, currentLt, err
		}
		if resp.Error != "" {
			return nil, currentUtime, currentLt, fmt.Errorf("toncenter messages: %s", resp.Error)
		}
		if len(resp.Messages) == 0 {
			break
		}

		for _, m := range resp.Messages {
			if m.MessageContent.Body == "" {
				continue
			}

			var lt int64
			if m.CreatedLT != "" {
				lt, _ = strconv.ParseInt(m.CreatedLT, 10, 64)
			}
			if m.CreatedAt != "" {
				if v, err := strconv.ParseInt(m.CreatedAt, 10, 64); err == nil && v > currentUtime {
					currentUtime = v
				}
			}
			if lt > currentLt {
				currentLt = lt
			}

			results = append(results, ProcessedTransaction{
				Hash:         decodeTxHash(m.OutMsgTxHash),
				LT:           lt,
				OutMsgBodies: []string{m.MessageContent.Body},
			})
		}

		if len(resp.Messages) < pageSize {
			break
		}
	}

	return results, currentUtime, currentLt, nil
}

// decodeTxHash converts TonCenter's base64 (44 chars w/ padding) message-tx
// hash to canonical lowercase hex (64 chars), which is the format DTON used
// historically and what existing OnchainLog rows already store. If the input
// is already 64-char hex it is returned lowercased; anything else is passed
// through unchanged so a future TonCenter format change does not silently
// corrupt the value.
func decodeTxHash(s string) string {
	switch len(s) {
	case 0:
		return ""
	case 64:
		if isHex(s) {
			return strings.ToLower(s)
		}
	case 44:
		if dec, err := base64.StdEncoding.DecodeString(s); err == nil && len(dec) == 32 {
			return hex.EncodeToString(dec)
		}
	}
	return s
}

func isHex(s string) bool {
	for _, c := range s {
		switch {
		case c >= '0' && c <= '9':
		case c >= 'a' && c <= 'f':
		case c >= 'A' && c <= 'F':
		default:
			return false
		}
	}
	return true
}
