package indexer

import (
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"sort"
	"testing"
	"time"

	sdkConfig "github.com/evaafi/evaa-go-sdk/config"
	sdkPrincipal "github.com/evaafi/evaa-go-sdk/principal"
	"github.com/evaafi/go-indexer/config"
	"github.com/xssnick/tonutils-go/address"
	"github.com/xssnick/tonutils-go/tvm/cell"
)

// Integration test: fetch state from TON Center and parse principals without mocks.
func TestTonCenterFetchAndParsePrincipals(t *testing.T) {
	cfg, err := config.LoadConfig("../config.yaml")
	if err != nil {
		t.Skipf("skip: cannot load config: %v", err)
	}
	if cfg.TonCenterAPIKey == "" {
		t.Skip("skip: toncenterApiKey not set in config.yaml")
	}

	// pick a wallet address and derive user contract in main pool
	svc := sdkPrincipal.NewService(sdkConfig.GetMainMainnetConfig())
	walletAddr := address.MustParseAddr("EQCiAvtDKDBy9hGF_AxGCsdrXax7wt7uNjYbxVls0C0RDPGp")
	userContractAddress, _ := svc.CalculateUserSCAddress(walletAddr)
	t.Logf("wallet: %s", walletAddr.String())
	t.Logf("user contract: %s", userContractAddress.String())

	started := time.Now()

	// Build and log request URLs for both wallet and contract
	uWallet, _ := url.Parse("https://toncenter.com/api/v3/accountStates")
	qW := uWallet.Query()
	qW.Add("address", walletAddr.String())
	qW.Set("include_boc", "true")
	uWallet.RawQuery = qW.Encode()
	t.Logf("GET (wallet)   %s", uWallet.String())
	uContract, _ := url.Parse("https://toncenter.com/api/v3/accountStates")
	qC := uContract.Query()
	qC.Add("address", userContractAddress.String())
	qC.Set("include_boc", "true")
	uContract.RawQuery = qC.Encode()
	t.Logf("GET (contract) %s", uContract.String())

	// Try wallet first
	resp, err := fetchTonCenterAccountStatesWithBaseURL("https://toncenter.com", cfg.TonCenterAPIKey, []string{walletAddr.String()})
	if err != nil {
		t.Fatalf("toncenter request failed: %v", err)
	}
	bocBase64 := resp[walletAddr.String()]
	// If empty, try contract
	if bocBase64 == "" {
		t.Log("wallet data_boc empty, trying contract address...")
		resp, err = fetchTonCenterAccountStatesWithBaseURL("https://toncenter.com", cfg.TonCenterAPIKey, []string{userContractAddress.String()})
		if err != nil {
			t.Fatalf("toncenter request (contract) failed: %v", err)
		}
		bocBase64 = resp[userContractAddress.String()]
		if bocBase64 == "" {
			// Raw GET like curl to inspect body
			u, _ := url.Parse("https://toncenter.com/api/v3/accountStates")
			q := u.Query()
			q.Add("address", walletAddr.String())
			q.Set("include_boc", "true")
			q.Set("api_key", cfg.TonCenterAPIKey)
			u.RawQuery = q.Encode()
			req, _ := http.NewRequest("GET", u.String(), nil)
			res, err2 := http.DefaultClient.Do(req)
			if err2 == nil && res != nil {
				defer res.Body.Close()
				b, _ := io.ReadAll(res.Body)
				t.Logf("raw body (wallet): %s", string(b))
			}
			t.Fatalf("empty data_boc for both wallet %s and contract %s", walletAddr.String(), userContractAddress.String())
		}
	}
	t.Logf("toncenter responded in %s", time.Since(started))
	t.Logf("data_boc base64 length: %d", len(bocBase64))

	b, err := base64.StdEncoding.DecodeString(bocBase64)
	if err != nil {
		t.Fatalf("base64 decode failed: %v", err)
	}
	t.Logf("boc bytes length: %d", len(b))
	c, err := cell.FromBOC(b)
	if err != nil {
		t.Fatalf("FromBOC failed: %v", err)
	}

	user := sdkPrincipal.NewUserSC(userContractAddress)
	_, _ = user.SetAccData(c)
	principals := user.Principals()
	if len(principals) == 0 {
		t.Fatalf("no principals parsed")
	}

	// Log summary
	// Note: code version and state are for parsed cell (user SC)
	t.Logf("code version: %d", user.CodeVersion())
	t.Logf("user state: %d", user.UserState())

	// human-readable names for known assets
	assetName := map[string]string{
		config.TonAssetId:           "ton",
		config.UsdtAssetId:          "usdt",
		config.StTonAssetId:         "stton",
		config.TsTonAssetId:         "tston",
		config.JusdtAssetId:         "jusdt",
		config.JusdcAssetId:         "jusdc",
		config.TonUsdtDedustAssetId: "tonusdt_dedust",
		config.TonStormAssetId:      "ton_storm",
		config.UsdtStormAssetId:     "usdt_storm",
		config.NotAssetId:           "not",
		config.DogsAssetId:          "dogs",
		config.CatiAssetId:          "cati",
		config.UsdeAssetId:          "usde",
		config.TsUsdeAssetId:        "tsusde",
	}

	keys := make([]string, 0, len(principals))
	for k := range principals {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		v := principals[k]
		name := assetName[k]
		mark := ""
		if v != nil && v.Sign() != 0 {
			mark = "*"
		}
		if name != "" {
			t.Logf("%s principal %s (%s) = %s", mark, name, k, v.String())
		} else {
			t.Logf("%s principal %s = %s", mark, k, v.String())
		}
	}
}

func TestTonCenterMessagesParsing(t *testing.T) {
	cfg, err := config.LoadConfig("../config.yaml")
	if err != nil {
		t.Skipf("skip: cannot load config: %v", err)
	}
	if cfg.TonCenterAPIKey == "" {
		t.Skip("skip: toncenterApiKey not set in config.yaml")
	}
	// use main pool as source
	source := config.PoolMain.Address
	u, _ := url.Parse("https://toncenter.com/api/v3/messages")
	q := u.Query()
	q.Add("source", source)
	q.Set("destination", "null")
	q.Set("limit", "10")
	q.Set("offset", "0")
	q.Set("sort", "desc")
	q.Set("api_key", cfg.TonCenterAPIKey)
	u.RawQuery = q.Encode()

	req, _ := http.NewRequest("GET", u.String(), nil)
	req.Header.Set("X-Api-Key", cfg.TonCenterAPIKey)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("messages request failed: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		b, _ := io.ReadAll(res.Body)
		t.Fatalf("messages status %d body: %s", res.StatusCode, string(b))
	}
	var parsed TonCenterMessagesResponse
	if err := json.NewDecoder(res.Body).Decode(&parsed); err != nil {
		t.Fatalf("decode json failed: %v", err)
	}
	t.Logf("messages returned: %d", len(parsed.Messages))
	if len(parsed.Messages) == 0 {
		t.Skip("no messages returned for source; skipping body parsing checks")
	}
	foundBody := false
	for _, m := range parsed.Messages {
		if m.OutMsgTxHash == "" {
			t.Fatalf("message has empty out_msg_tx_hash")
		}
		if dec, err := base64.StdEncoding.DecodeString(m.OutMsgTxHash); err == nil {
			t.Logf("out_msg_tx_hash hex: %s", hex.EncodeToString(dec))
		}
		if m.MessageContent.Body != "" && !foundBody {
			if _, err := base64.StdEncoding.DecodeString(m.MessageContent.Body); err != nil {
				t.Fatalf("message body is not base64: %v", err)
			}
			foundBody = true
			// Log the hash we will use downstream
			t.Logf("sample out_msg_tx_hash (base64): %s", m.OutMsgTxHash)
			if dec, err := base64.StdEncoding.DecodeString(m.OutMsgTxHash); err == nil {
				t.Logf("sample out_msg_tx_hash (hex): %s", hex.EncodeToString(dec))
			}
		}
	}
	if !foundBody {
		t.Log("no message bodies present in first page; this can happen depending on block timing")
	}
}

func TestParseTonCenterMessagesSample(t *testing.T) {
	sample := `{
		"messages": [
			{
				"hash": "yvRrBw4i+S0nNmXU18xEFdJs6bqaoI/3ub6N5ecoJfk=",
				"source": "0:BCAD466A47FA565750729565253CD073CA24D856804499090C2100D95C809F9E",
				"destination": null,
				"created_lt": "60505066000009",
				"created_at": "1755293452",
				"out_msg_tx_hash": "AdhzLJzUq/TfP30M1JJhclw42RN2c3ceW+e0HVeT6b0=",
				"message_content": {
					"hash": "0qkyuK0NWOyPGg39cHuCwVRPOqrOEfTyKycxhTcP/Q4=",
					"body": "te6cckEBAwEAwgAC0wKADxXzN6mbZIBlffFWkwhb7HuL2HI+c+enBWtpKvBmQDaQA/5r9+3BuQtvts58lPXP/CmCf4PLiyMxLQ3z0ECjJDMKADxXzN6mbZIBlffFWkwhb7HuL2HI+c+enBWtpKvBmQDaNE/ThkABAgAAAKAaQhn+XmDWOvKjzH3Ob+xptFxrVxhJemFI58IyrIe9igAAAAAzw0Nt/////8J4+u0ABw6Ts8bmGwAErwWDStL3AAAAvsV60PkAAADD4R/PCeMM2T4="
				}
			}
		],
		"address_book": {
			"0:BCAD466A47FA565750729565253CD073CA24D856804499090C2100D95C809F9E": {
				"user_friendly": "EQC8rUZqR_pWV1BylWUlPNBzyiTYVoBEmQkMIQDZXICfnuRr",
				"domain": null
			}
		},
		"metadata": {}
	}`
	var parsed TonCenterMessagesResponse
	if err := json.Unmarshal([]byte(sample), &parsed); err != nil {
		t.Fatalf("failed to unmarshal sample: %v", err)
	}
	if len(parsed.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(parsed.Messages))
	}
	m := parsed.Messages[0]
	if m.OutMsgTxHash != "AdhzLJzUq/TfP30M1JJhclw42RN2c3ceW+e0HVeT6b0=" {
		t.Fatalf("unexpected out_msg_tx_hash: %s", m.OutMsgTxHash)
	}
	if dec, err := base64.StdEncoding.DecodeString(m.OutMsgTxHash); err == nil {
		t.Logf("out_msg_tx_hash hex: %s", hex.EncodeToString(dec))
	}
	if m.MessageContent.Body == "" {
		t.Fatalf("empty message body")
	}
	if _, err := base64.StdEncoding.DecodeString(m.MessageContent.Body); err != nil {
		t.Fatalf("body not base64: %v", err)
	}
	// Try to parse via our log parser to see type/subtype like in prod indexer
	idxLog, err := ParseLogMessage(m.MessageContent.Body, 1)
	if err != nil {
		// If unknown or version mismatch, just log and don't fail
		t.Logf("parser returned error: %v", err)
		return
	}
	// Map tx hash from message into idxLog for display, using hex
	if dec, err := base64.StdEncoding.DecodeString(m.OutMsgTxHash); err == nil {
		idxLog.Hash = hex.EncodeToString(dec)
	}
	toStr := func(b config.BigInt) string {
		if b.Int == nil {
			return "0"
		}
		return b.String()
	}
	t.Logf("All fields: hash=%s pool=%s utime=%d type=%s subtype=%s sender=%s user=%s created_at=%s",
		idxLog.Hash, idxLog.Pool, idxLog.Utime, idxLog.TxType, idxLog.TxSubType, idxLog.SenderAddress, idxLog.UserAddress, idxLog.CreatedAt.String())
	t.Logf("Attached: asset=%s amount=%s principal=%s s_total_sup=%s s_total_bor=%s s_rate=%s b_rate=%s",
		toStr(idxLog.AttachedAssetAddress), toStr(idxLog.AttachedAssetAmount), toStr(idxLog.AttachedAssetPrincipal),
		toStr(idxLog.AttachedAssetTotalSupplyPrincipal), toStr(idxLog.AttachedAssetTotalBorrowPrincipal),
		toStr(idxLog.AttachedAssetSRate), toStr(idxLog.AttachedAssetBRate))
	t.Logf("Redeemed: asset=%s amount=%s principal=%s s_total_sup=%s s_total_bor=%s s_rate=%s b_rate=%s",
		toStr(idxLog.RedeemedAssetAddress), toStr(idxLog.RedeemedAssetAmount), toStr(idxLog.RedeemedAssetPrincipal),
		toStr(idxLog.RedeemedAssetTotalSupplyPrincipal), toStr(idxLog.RedeemedAssetTotalBorrowPrincipal),
		toStr(idxLog.RedeemedAssetSRate), toStr(idxLog.RedeemedAssetBRate))
}
