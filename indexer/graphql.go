package indexer

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"

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
	} `json:"data"`
	Errors []interface{} `json:"errors"`
}

type ProcessedTransaction struct {
	Hash         string   `json:"hash"`
	LT           int64    `json:"lt"`
	OutMsgBodies []string `json:"out_msg_body"`
}

// out_msg_op_code
func GetRawTransactions(url, address string, page_size, page int, utimeStart, utimeEnd int64) (string, error) {
	/*query := fmt.Sprintf(`
		{
		raw_transactions(
			order_by: "lt",
			address_friendly: "%s",
			out_msg_type__has: "ext_out_msg_info",
			page_size: %d
			page: %d
			lt_gte: "%s"
		) {
			lt
			hash
			out_msg_type
			out_msg_body
			out_msg_dest_addr_address_hex
		}
	}`, address, page_size, page, lt)*/
	query := fmt.Sprintf(`
	{
	raw_transactions(
		order_by: "gen_utime",
		address_friendly: "%s",
		out_msg_type__has: "ext_out_msg_info",
		page_size: %d
		page: %d
		gen_utime__gt: "%d",
		gen_utime__lte: "%d",
	) {
		lt
		gen_utime__utc_unix
		hash
		out_msg_type
		out_msg_body
		out_msg_dest_addr_address_hex
	}
}`, address, page_size, page, utimeStart, utimeEnd)
	/*query := fmt.Sprintf(`
	  {
	  raw_transactions(
	  	order_by: "gen_utime",
	  	address_friendly: "%s",
	  	out_msg_type__has: "ext_out_msg_info",
	  	page_size: %d
	  	page: %d
	  	gen_utime__gt: "%d"
	  	) {
	  	lt
	  	gen_utime__utc_unix
	  	hash
	  	out_msg_type
	  	out_msg_body
	  	out_msg_dest_addr_address_hex
	  }
	  }`, address, page_size, page, utimeStart)*/

	/*file, err := os.OpenFile("queries.txt", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	defer file.Close()
	_, err = file.WriteString(query)

	fmt.Println("File written successfully!")*/
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
		log.Printf("error in req: %v", err)
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		log.Printf("error in sending http requset: %v", err)
		return "", err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Printf("error in receiving body: %v", err)
		return "", err
	}

	return string(body), nil
}

func GetRawState(url, userContractAddress string) (string, error) {
	query := fmt.Sprintf(`
	{
		raw_account_states(
			address__friendly: "%s"
		) {
			account_state_state_init_data
		}
	}`, userContractAddress)

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
		log.Printf("error in req: %v", err)
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		log.Printf("error in sending http requset: %v", err)
		return "", err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Printf("error in receiving body: %v", err)
		return "", err
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

	for {
		if errors == 5 {
			return nil, currentUtime, fmt.Errorf("ProcessTransactions errors counter > 5")
		}

		responseStr, err := GetRawTransactions(url, address, pageSize, page, initialUtime, initialUtime+config.UtimeAddendum)
		if err != nil {
			fmt.Println(err)
			errors++
			continue
		}

		var gqlResp GraphQLTransactionsResponse
		if err := json.Unmarshal([]byte(responseStr), &gqlResp); err != nil {
			fmt.Println(err)
			errors++
			continue
		}

		page++
		errors = 0
		transactions := gqlResp.Data.RawTransactions
		if len(transactions) == 0 {
			// fmt.Println(responseStr);
			break
		}

		for _, tx := range transactions {
			var bodies []string
			for idx, msgType := range tx.OutMsgType {
				if msgType == "ext_out_msg_info" {
					if idx < len(tx.OutMsgBody) {
						bodies = append(bodies, tx.OutMsgBody[idx])
						// fmt.Println("found body")
					}
				}
			}
			processed := ProcessedTransaction{
				Hash:         tx.Hash,
				LT:           tx.LT,
				OutMsgBodies: bodies,
			}

			currentUtime = max(currentUtime, tx.Utime)
			results = append(results, processed)
		}
	}

	return results, currentUtime, nil
}
