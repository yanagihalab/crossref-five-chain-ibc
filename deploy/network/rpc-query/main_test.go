package main

import (
	"encoding/json"
	"testing"
)

func TestLiveParamsPayload(t *testing.T) {
	value, err := decode("CgoIARCQTiDoBygK")
	if err != nil {
		t.Fatal(err)
	}
	var response struct {
		Params map[string]interface{} `json:"params"`
	}
	if err := json.Unmarshal(value, &response); err != nil {
		t.Fatal(err)
	}
	if response.Params["require_consensus_proof"] != false || response.Params["require_strict_hysteresis_signature"] != true || response.Params["checkpoint_proof_max_lag"] != "10000" {
		t.Fatalf("unexpected params: %s", value)
	}
	for _, bad := range []string{"", "not base64", "////"} {
		if _, err := decode(bad); err == nil {
			t.Fatalf("accepted %q", bad)
		}
	}
}
