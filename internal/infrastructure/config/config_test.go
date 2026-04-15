package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/seu-usuario/project-sentinel/internal/domain"
)

func TestValidateRejectsShortAndDuplicateKeys(t *testing.T) {
	modelsPath := writeTempModelsConfig(t)
	cfg := Config{
		AppEnv:                      "production",
		HTTPAddr:                    ":8080",
		SessionStorePath:            t.TempDir(),
		ModelsConfigPath:            modelsPath,
		RotationStrategy:            domain.RotationQuotaFirst,
		DefaultModel:                "sentinel-router",
		DefaultReasoningEffort:      "high",
		RequestTimeoutSeconds:       30,
		MaxAttempts:                 3,
		QuotaRefreshIntervalSeconds: 0,
		SessionEncryptionKey:        "12345678901234567890123456789012",
		APIKey:                      "short",
		AdminAPIKey:                 "short",
	}
	if err := cfg.Validate(); err == nil {
		t.Fatalf("expected validation error for weak keys")
	}
}

func TestValidateAcceptsDistinctProductionKeys(t *testing.T) {
	modelsPath := writeTempModelsConfig(t)
	cfg := Config{
		AppEnv:                      "production",
		HTTPAddr:                    ":8080",
		SessionStorePath:            t.TempDir(),
		ModelsConfigPath:            modelsPath,
		RotationStrategy:            domain.RotationQuotaFirst,
		DefaultModel:                "sentinel-router",
		DefaultReasoningEffort:      "high",
		RequestTimeoutSeconds:       30,
		MaxAttempts:                 3,
		QuotaRefreshIntervalSeconds: 0,
		SessionEncryptionKey:        "12345678901234567890123456789012",
		APIKey:                      "example-sentinel-api-key-012345678901234567890",
		AdminAPIKey:                 "example-sentinel-admin-key-abcdefghijklmnopqrstuvwxyz",
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("expected config to validate, got %v", err)
	}
}

func writeTempModelsConfig(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "models.json")
	content := []byte(`{"models":[{"id":"sentinel-router","provider":"chatgpt","upstream_model":"gpt-5.4"}]}`)
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatalf("write temp models config: %v", err)
	}
	return path
}
