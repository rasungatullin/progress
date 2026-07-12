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
