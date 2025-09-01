package indexer

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	//"os"
	"github.com/evaafi/go-indexer/config"
)

type Transaction struct {
	LT                       int64    `json:"lt"`
	Utime                    int64    `json:"gen_utime__utc_unix"`
	Hash                     string   `json:"hash"`
	OutMsgOpCode             []int64  `json:"out_msg_op_code"`
	OutMsgType               []string `json:"out_msg_type"`
	OutMsgBody               []string `json:"out_msg_body"`
	OutMsgDestAddrAddressHex []string `json:"out_msg_dest_addr_address_hex"`
}

func (t *Transaction) UnmarshalJSON(data []byte) error {
	type Alias Transaction
	aux := &struct {
		RawLT   json.Number `json:"lt"`
		RawUnix json.Number `json:"gen_utime__utc_unix"`
		//RawOutMsgOpCode []json.Number `json:"out_msg_op_code"`
		*Alias
	}{
		Alias: (*Alias)(t),
	}

	if err := json.Unmarshal(data, aux); err != nil {
		return err
	}

	if aux.RawLT != "" {
		ltInt, err := strconv.ParseFloat(aux.RawLT.String(), 64)
		if err != nil {
			return fmt.Errorf("error converting lt int int64: %w", err)
		}
		t.LT = int64(ltInt)
	}

	if aux.RawUnix != "" {
		utimeInt, err := strconv.ParseInt(aux.RawUnix.String(), 10, 64)
		if err != nil {
			return fmt.Errorf("error converting utimeInt int int64: %w", err)
		}
		t.Utime = int64(utimeInt)
	}

	/*t.OutMsgOpCode = make([]int64, len(aux.RawOutMsgOpCode))
	for i, num := range aux.RawOutMsgOpCode {
		val, err := strconv.ParseFloat(num.String(), 64)
		if err != nil {
			return fmt.Errorf("error converting out_msg_op_code to int: %w '%s'", err, num.String())
		}
		t.OutMsgOpCode[i] = int64(val)
	}*/

	return nil
}

type State struct {
	State string `json:"account_state_state_init_data"`
}

type GraphQLTransactionsResponse struct {
	Data struct {
		RawTransactions []Transaction `json:"raw_transactions"`
	} `json:"data"`
	Errors []interface{} `json:"errors"`
}

type GraphQLStatesResponse struct {
	Data struct {
		RawAccountStates []State `json:"raw_account_states"`
		//RawAccountStates []State `json:"raw_transactions"`
	} `json:"data"`
	Errors []interface{} `json:"errors"`
}

type ProcessedTransaction struct {
	Hash         string   `json:"hash"`
	LT           int64    `json:"lt"`
	OutMsgBodies []string `json:"out_msg_body"`
}

// TON Center /api/v3/messages structures
type TonCenterMessage struct {
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

type TonCenterMessagesResponse struct {
	Messages    []TonCenterMessage `json:"messages"`
	AddressBook map[string]struct {
		UserFriendly string  `json:"user_friendly"`
		Domain       *string `json:"domain"`
	} `json:"address_book"`
	Metadata map[string]any `json:"metadata"`
}

// out_msg_op_code
func GetRawTransactions(_ string, address string, page_size, page int, startUtime int64, endUtime int64) (string, error) {
	// apply shared TON Center rate limiter
	baseURL := "https://toncenter.com"
	endpoint := strings.TrimRight(baseURL, "/") + "/api/v3/messages"
	u, err := url.Parse(endpoint)
	if err != nil {
		return "", fmt.Errorf("failed to parse endpoint %s: %w", endpoint, err)
	}
	if page_size <= 0 {
		page_size = 10
	}
	if page_size > 1000 {
		page_size = 1000
	}
	q := u.Query()
	q.Add("source", address)
	q.Set("destination", "null")
	q.Set("limit", strconv.Itoa(page_size))
	q.Set("offset", strconv.Itoa(page*page_size))
	q.Set("sort", "asc")
	if startUtime > 0 {
		q.Set("start_utime", strconv.FormatInt(startUtime, 10))
	}
	if endUtime > 0 {
		q.Set("end_utime", strconv.FormatInt(endUtime, 10))
	}
	if config.CFG.TonCenterAPIKey != "" {
		q.Set("api_key", config.CFG.TonCenterAPIKey)
	}
	u.RawQuery = q.Encode()

	// build a sanitized URL for logs (no api_key in query)
	safeURL := *u
	qSafe := safeURL.Query()
	qSafe.Del("api_key")
	safeURL.RawQuery = qSafe.Encode()

	req, err := http.NewRequest("GET", u.String(), nil)
	if err != nil {
		log.Printf("error creating request GET %s: %v", safeURL.String(), err)
		return "", fmt.Errorf("create request GET %s: %w", safeURL.String(), err)
	}
	if config.CFG.TonCenterAPIKey != "" {
		req.Header.Set("X-Api-Key", config.CFG.TonCenterAPIKey)
	}

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		log.Printf("error sending HTTP request GET %s: %v", safeURL.String(), err)
		return "", fmt.Errorf("GET %s failed: %w", safeURL.String(), err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("GET %s status %d body: %s", safeURL.String(), resp.StatusCode, string(b))
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Printf("error reading response body from GET %s: %v", safeURL.String(), err)
		return "", fmt.Errorf("read body GET %s: %w", safeURL.String(), err)
	}

	return string(body), nil
}

func GetRawState(url, userContractAddress string) (string, error) {
	query := fmt.Sprintf(`
	{
		raw_account_states(
			address__friendly: "%s"
			page_size: 1
			page: 0
		) {
			account_state_state_init_data
		}
		
	}`, userContractAddress)
	/*query := fmt.Sprintf(`
	{
		raw_transactions(
			address_friendly: "%s"
			page_size: 1
			page: 0
		) {
			account_state_state_init_data
		}
	}`, userContractAddress)*/

	payload := map[string]string{
		"query": query,
	}
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		log.Printf("error in payload: %v", err)
		return "", err
	}

	req, err := http.NewRequest("POST", url, bytes.NewBuffer(payloadBytes))
	if err != nil {
		log.Printf("error creating request POST %s: %v", url, err)
		return "", fmt.Errorf("create request POST %s: %w", url, err)
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		log.Printf("error sending HTTP request POST %s: %v", url, err)
		return "", fmt.Errorf("POST %s failed: %w", url, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Printf("error reading response body from POST %s: %v", url, err)
		return "", fmt.Errorf("read body POST %s: %w", url, err)
	}

	return string(body), nil
}

func GetAccountState(url, userContractAddress string) (GraphQLStatesResponse, error) {
	errors := 0

	for {
		if errors == 5 {
			return GraphQLStatesResponse{}, fmt.Errorf("GetAccountState errors counter > 5")
		}

		responseStr, err := GetRawState(url, userContractAddress)

		if err != nil {
			errors++
			continue
		}

		var gqlResp GraphQLStatesResponse
		if err := json.Unmarshal([]byte(responseStr), &gqlResp); err != nil {
			errors++
			continue
		}

		return gqlResp, nil
	}
}

func ProcessTransactions(url, address string, initialUtime int64, pageSize int) ([]ProcessedTransaction, int64, error) {
	var results []ProcessedTransaction
	currentUtime := initialUtime
	page := 0
	errors := 0

	// adaptive window: start strictly AFTER last processed utime to avoid boundary duplicates
	windowStart := initialUtime
	windowEnd := initialUtime + config.UtimeAddendum

	for {
		if errors == 5 {
			return nil, currentUtime, fmt.Errorf("ProcessTransactions errors counter > 5")
		}

		responseStr, err := GetRawTransactions(url, address, pageSize, page, windowStart, windowEnd)
		if err != nil {
			// if first page fails, shrink window end by half of current span and retry immediately
			if page == 0 {
				span := windowEnd - windowStart
				if span > 1 {
					windowEnd = windowEnd - span/2
					continue
				}
			}
			fmt.Println(err)
			errors++
			continue
		}

		var msgResp TonCenterMessagesResponse
		if err := json.Unmarshal([]byte(responseStr), &msgResp); err != nil {
			fmt.Println(err)
			errors++
			continue
		}

		page++
		errors = 0
		messages := msgResp.Messages
		if len(messages) == 0 {
			// fmt.Println(responseStr);
			break
		}

		for _, m := range messages {
			var bodies []string
			if m.MessageContent.Body != "" {
				bodies = append(bodies, m.MessageContent.Body)
			}
			var ltParsed int64
			if m.CreatedLT != "" {
				if v, err := strconv.ParseInt(m.CreatedLT, 10, 64); err == nil {
					ltParsed = v
				}
			}
			if m.CreatedAt != "" {
				if v, err := strconv.ParseInt(m.CreatedAt, 10, 64); err == nil {
					currentUtime = max(currentUtime, v)
				}
			}
			// convert base64 tx hash to hex for storage
			hashHex := m.OutMsgTxHash
			if dec, err := base64.StdEncoding.DecodeString(m.OutMsgTxHash); err == nil {
				hashHex = fmt.Sprintf("%x", dec)
			}
			processed := ProcessedTransaction{
				Hash:         hashHex,
				LT:           ltParsed,
				OutMsgBodies: bodies,
			}
			results = append(results, processed)
		}
	}

	return results, currentUtime, nil
}
