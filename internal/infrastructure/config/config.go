package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"github.com/joho/godotenv"
	"github.com/seu-usuario/project-sentinel/internal/domain"
)

type Config struct {
	AppEnv                      string
	HTTPAddr                    string
	SessionStorePath            string
	StateDBPath                 string
	ModelsConfigPath            string
	LogLevel                    string
	RotationStrategy            domain.RotationStrategy
	DefaultModel                string
	DefaultReasoningEffort      string
	QuotaRefreshIntervalSeconds int
	RequestTimeoutSeconds       int
	MaxAttempts                 int
	SessionEncryptionKey        string
	APIKey                      string
}

func Load() (Config, error) {
	if err := godotenv.Load(); err != nil && !os.IsNotExist(err) {
		return Config{}, fmt.Errorf("load .env: %w", err)
	}

	requestTimeoutSeconds, err := envInt("REQUEST_TIMEOUT_SECONDS", 30)
	if err != nil {
		return Config{}, err
	}
	maxAttempts, err := envInt("MAX_ATTEMPTS", 3)
	if err != nil {
		return Config{}, err
	}
	quotaRefreshIntervalSeconds, err := envInt("QUOTA_REFRESH_INTERVAL_SECONDS", 300)
	if err != nil {
		return Config{}, err
	}
	rotationStrategy, err := domain.ParseRotationStrategy(env("ROTATION_STRATEGY", string(domain.RotationQuotaFirst)))
	if err != nil {
		return Config{}, fmt.Errorf("config validation failed: %w", err)
	}
	defaultReasoningEffort := env("DEFAULT_REASONING_EFFORT", "high")
	cfg := Config{
		AppEnv:                      env("APP_ENV", "development"),
		HTTPAddr:                    env("HTTP_ADDR", ":8080"),
		SessionStorePath:            env("SESSION_STORE_PATH", "./sessions"),
		StateDBPath:                 env("STATE_DB_PATH", "./sessions/state.db"),
		ModelsConfigPath:            env("MODELS_CONFIG_PATH", "./configs/models.json"),
		LogLevel:                    env("LOG_LEVEL", "info"),
		RotationStrategy:            rotationStrategy,
		DefaultModel:                env("DEFAULT_MODEL", "sentinel-router"),
		DefaultReasoningEffort:      defaultReasoningEffort,
		QuotaRefreshIntervalSeconds: quotaRefreshIntervalSeconds,
		RequestTimeoutSeconds:       requestTimeoutSeconds,
		MaxAttempts:                 maxAttempts,
		SessionEncryptionKey:        os.Getenv("SESSION_ENCRYPTION_KEY"),
		APIKey:                      os.Getenv("SENTINEL_API_KEY"),
	}

	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}

	return cfg, nil
}

func (c Config) Validate() error {
	if c.HTTPAddr == "" {
		return fmt.Errorf("config validation failed: HTTP_ADDR is required")
	}
	if c.SessionStorePath == "" {
		return fmt.Errorf("config validation failed: SESSION_STORE_PATH is required")
	}

	if err := ensureWritableDirectory(c.SessionStorePath); err != nil {
		return err
	}

	if c.ModelsConfigPath == "" {
		return fmt.Errorf("config validation failed: MODELS_CONFIG_PATH is required")
	}
	if _, err := os.Stat(c.ModelsConfigPath); err != nil {
		return fmt.Errorf("config validation failed: MODELS_CONFIG_PATH is not accessible: %w", err)
	}

	if c.AppEnv == "production" && c.SessionEncryptionKey == "" {
		return fmt.Errorf("config validation failed: SESSION_ENCRYPTION_KEY is required in production")
	}
	if c.AppEnv == "production" && c.APIKey == "" {
		return fmt.Errorf("config validation failed: SENTINEL_API_KEY is required in production")
	}

	if c.RequestTimeoutSeconds <= 0 {
		return fmt.Errorf("config validation failed: REQUEST_TIMEOUT_SECONDS must be greater than zero")
	}

	if c.MaxAttempts < 1 || c.MaxAttempts > 5 {
		return fmt.Errorf("config validation failed: MAX_ATTEMPTS must be between 1 and 5")
	}
	if c.QuotaRefreshIntervalSeconds < 0 {
		return fmt.Errorf("config validation failed: QUOTA_REFRESH_INTERVAL_SECONDS must be zero or greater")
	}
	if _, err := domain.ParseRotationStrategy(string(c.RotationStrategy)); err != nil {
		return fmt.Errorf("config validation failed: %w", err)
	}
	if c.DefaultModel == "" {
		return fmt.Errorf("config validation failed: DEFAULT_MODEL is required")
	}
	switch c.DefaultReasoningEffort {
	case "auto", "high", "xhigh":
	default:
		return fmt.Errorf("config validation failed: DEFAULT_REASONING_EFFORT must be auto, high or xhigh")
	}

	return nil
}

func env(name string, fallback string) string {
	value := os.Getenv(name)
	if value == "" {
		return fallback
	}

	return value
}

func envInt(name string, fallback int) (int, error) {
	value := os.Getenv(name)
	if value == "" {
		return fallback, nil
	}

	parsed, err := strconv.Atoi(value)
	if err != nil {
		return 0, fmt.Errorf("config validation failed: %s must be an integer", name)
	}

	return parsed, nil
}

func ensureWritableDirectory(path string) error {
	if err := os.MkdirAll(path, 0o700); err != nil {
		return fmt.Errorf("config validation failed: SESSION_STORE_PATH cannot be created: %w", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("config validation failed: SESSION_STORE_PATH is not accessible: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("config validation failed: SESSION_STORE_PATH is not a directory")
	}

	probe := filepath.Join(path, ".write_test")
	file, err := os.OpenFile(probe, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("config validation failed: SESSION_STORE_PATH is not writable: %w", err)
	}
	if _, err := file.Write([]byte("ok")); err != nil {
		closeErr := file.Close()
		if closeErr != nil {
			return fmt.Errorf("config validation failed: SESSION_STORE_PATH write failed: %w; close failed: %v", err, closeErr)
		}
		return fmt.Errorf("config validation failed: SESSION_STORE_PATH write failed: %w", err)
	}
	if err := file.Sync(); err != nil {
		closeErr := file.Close()
		if closeErr != nil {
			return fmt.Errorf("config validation failed: SESSION_STORE_PATH sync failed: %w; close failed: %v", err, closeErr)
		}
		return fmt.Errorf("config validation failed: SESSION_STORE_PATH sync failed: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("config validation failed: SESSION_STORE_PATH close failed: %w", err)
	}
	if err := os.Remove(probe); err != nil {
		return fmt.Errorf("config validation failed: SESSION_STORE_PATH cleanup failed: %w", err)
	}

	return nil
}
