package indexer_test

import (
	"encoding/json"
	"testing"

	"github.com/evaafi/go-indexer/config"
	"github.com/evaafi/go-indexer/indexer"
)

func getData(cfg config.Config, st string) {
	rawState, _ := indexer.GetRawState(cfg.GraphQLEndpoint, st)
	var userStateResponse indexer.GraphQLStatesResponse
	if err := json.Unmarshal([]byte(rawState), &userStateResponse); err != nil {
		println("failed to unmarshal user state %s", rawState)
		return
	}

	println(userStateResponse.Data.RawAccountStates[0].State)

	if len(userStateResponse.Data.RawAccountStates) == 0 {
		println("cannot get user state: %s; adding again to queue", st)
		return
	}

}
func TestGetRawState(t *testing.T) {
	cfg, _ := config.LoadConfig("../config.yaml")

	getData(cfg, "EQDNSnDXSrvfZyEVQ6vaAYHKakqyKE2zbCKQc2JNY-AhbGpa")
	getData(cfg, "EQD1_i5tUQ-0SrKKRZf588f1CY8E9GDt20eNsH_01acgBiWE")
	getData(cfg, "EQBR2DQ0olmo0QEFeELUWcnGnAJXeqTV4ZKqM3MFnpsJRg51")
	getData(cfg, "EQCyno3tNXQIoJSWLURor_FNGZRilcVqUjeLOLMZF63PMzsg")
}
