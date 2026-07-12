package model

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestIntegrationSystemConfigReadsLegacyTokenWithoutSerializingIt(t *testing.T) {
	const actualToken = "legacy-secret-token"

	var config IntegrationSystemConfig
	if err := json.Unmarshal([]byte(`{"type":"github","token":"`+actualToken+`"}`), &config); err != nil {
		t.Fatalf("unmarshal legacy integration config: %v", err)
	}
	if config.Token != actualToken {
		t.Fatalf("unexpected legacy token: %q", config.Token)
	}

	encoded, err := json.Marshal(config)
	if err != nil {
		t.Fatalf("marshal integration config: %v", err)
	}
	if strings.Contains(string(encoded), actualToken) || strings.Contains(string(encoded), `"token"`) {
		t.Fatalf("serialized integration config contains actual token: %s", encoded)
	}
}
