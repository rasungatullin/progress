package model

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestIntegrationSystemConfigRejectsLegacyTokenWithMigrationMessage(t *testing.T) {
	const actualToken = "legacy-secret-token"

	var config IntegrationSystemConfig
	err := json.Unmarshal([]byte(`{"type":"github","token":"`+actualToken+`"}`), &config)
	if err == nil || !strings.Contains(err.Error(), "token_private") {
		t.Fatalf("expected migration message for legacy token, got %v", err)
	}
}

func TestIntegrationSystemConfigDoesNotSerializePrivateValues(t *testing.T) {
	config := IntegrationSystemConfig{
		Token:                      "actual-token",
		GitHubAppPrivateKey:        "actual-private-key",
		TokenPrivate:               "token-reference",
		GitHubAppPrivateKeyPrivate: "key-reference",
	}

	encoded, err := json.Marshal(config)
	if err != nil {
		t.Fatalf("marshal integration system config: %v", err)
	}
	output := string(encoded)
	for _, value := range []string{"actual-token", "actual-private-key"} {
		if strings.Contains(output, value) {
			t.Fatalf("serialized configuration contains private value %q: %s", value, output)
		}
	}
	for _, reference := range []string{"token-reference", "key-reference"} {
		if !strings.Contains(output, reference) {
			t.Fatalf("serialized configuration does not contain private value reference %q: %s", reference, output)
		}
	}
}
