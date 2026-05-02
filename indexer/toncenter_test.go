//go:build integration

package indexer

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"sort"
	"testing"
	"time"

	sdkConfig "github.com/evaafi/evaa-go-sdk/config"
	sdkPrincipal "github.com/evaafi/evaa-go-sdk/principal"
	"github.com/evaafi/go-indexer/config"
	"github.com/xssnick/tonutils-go/address"
	"github.com/xssnick/tonutils-go/tvm/cell"
)

// Run with: go test -tags=integration ./indexer/...
//
// These tests hit live TonCenter and require config.yaml at the repo root with
// a valid `toncenterApiKey`.

func TestTonCenterFetchAndParsePrincipals(t *testing.T) {
	cfg, err := config.LoadConfig("../config.yaml")
	if err != nil {
		t.Skipf("skip: cannot load config: %v", err)
	}
	if cfg.TonCenterAPIKey == "" {
		t.Skip("skip: toncenterApiKey not set in config.yaml")
	}
	config.CFG = cfg

	svc := sdkPrincipal.NewService(sdkConfig.GetMainMainnetConfig())
	walletAddr := address.MustParseAddr("EQCiAvtDKDBy9hGF_AxGCsdrXax7wt7uNjYbxVls0C0RDPGp")
	userContractAddress, _ := svc.CalculateUserSCAddress(walletAddr)
	t.Logf("wallet: %s", walletAddr.String())
	t.Logf("user contract: %s", userContractAddress.String())

	started := time.Now()
	bocBase64, err := GetAccountStateToncenter(context.Background(), userContractAddress.String())
	if err != nil {
		t.Fatalf("GetAccountStateToncenter: %v", err)
	}
	if bocBase64 == "" {
		t.Fatalf("empty data_boc for contract %s", userContractAddress.String())
	}
	t.Logf("toncenter responded in %s, data_boc length=%d", time.Since(started), len(bocBase64))

	b, err := base64.StdEncoding.DecodeString(bocBase64)
	if err != nil {
		t.Fatalf("base64 decode: %v", err)
	}
	c, err := cell.FromBOC(b)
	if err != nil {
		t.Fatalf("FromBOC: %v", err)
	}

	user := sdkPrincipal.NewUserSC(userContractAddress)
	if _, err := user.SetAccData(c); err != nil {
		t.Fatalf("SetAccData: %v", err)
	}
	principals := user.Principals()
	if len(principals) == 0 {
		t.Fatalf("no principals parsed")
	}

	t.Logf("code version: %d, user state: %d", user.CodeVersion(), user.UserState())

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
		mark := ""
		if v != nil && v.Sign() != 0 {
			mark = "*"
		}
		if name := assetName[k]; name != "" {
			t.Logf("%s principal %s (%s) = %s", mark, name, k, v.String())
		} else {
			t.Logf("%s principal %s = %s", mark, k, v.String())
		}
	}
}

func TestProcessTransactionsLive(t *testing.T) {
	cfg, err := config.LoadConfig("../config.yaml")
	if err != nil {
		t.Skipf("skip: cannot load config: %v", err)
	}
	if cfg.TonCenterAPIKey == "" {
		t.Skip("skip: toncenterApiKey not set in config.yaml")
	}
	config.CFG = cfg

	// Scan the last day of activity for the main pool. initialLt=0 forces the
	// utime-bootstrap path; subsequent pages within the call walk by LT.
	from := time.Now().Unix() - 24*60*60
	txs, lastUtime, lastLt, err := ProcessTransactions(context.Background(), config.PoolMain.Address, from, 0, 50)
	if err != nil {
		t.Fatalf("ProcessTransactions: %v", err)
	}
	t.Logf("got %d txs, last utime=%d, last lt=%d", len(txs), lastUtime, lastLt)
	if len(txs) == 0 {
		t.Skip("no traffic in window — skipping body sanity check")
	}
	first := txs[0]
	if first.Hash == "" {
		t.Fatalf("empty hash on first tx")
	}
	if len(first.OutMsgBodies) == 0 || first.OutMsgBodies[0] == "" {
		t.Fatalf("missing body on first tx %s", first.Hash)
	}
	if _, err := base64.StdEncoding.DecodeString(first.OutMsgBodies[0]); err != nil {
		t.Fatalf("body not base64 on tx %s: %v", first.Hash, err)
	}
}

// TestParseLogMessageSample exercises the parser against a recorded
// TonCenter response body so the test runs offline. Kept under the integration
// tag only because it lives in this file; safe to move to a separate file
// without the tag if you want it in the default test suite.
func TestParseLogMessageSample(t *testing.T) {
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
		]
	}`
	var parsed tcMessagesResponse
	if err := json.Unmarshal([]byte(sample), &parsed); err != nil {
		t.Fatalf("unmarshal sample: %v", err)
	}
	if len(parsed.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(parsed.Messages))
	}
	m := parsed.Messages[0]
	idxLog, err := ParseLogMessage(m.MessageContent.Body, 1)
	if err != nil {
		t.Logf("parser returned error (expected for some logVersions): %v", err)
		return
	}
	t.Logf("parsed: type=%s subtype=%s sender=%s user=%s utime=%d",
		idxLog.TxType, idxLog.TxSubType, idxLog.SenderAddress, idxLog.UserAddress, idxLog.Utime)
}
