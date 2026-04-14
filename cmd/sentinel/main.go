package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/seu-usuario/project-sentinel/internal/adapter"
	httpdelivery "github.com/seu-usuario/project-sentinel/internal/delivery/http"
	"github.com/seu-usuario/project-sentinel/internal/domain"
	"github.com/seu-usuario/project-sentinel/internal/infrastructure/config"
	sentinelcrypto "github.com/seu-usuario/project-sentinel/internal/infrastructure/crypto"
	"github.com/seu-usuario/project-sentinel/internal/infrastructure/logger"
	"github.com/seu-usuario/project-sentinel/internal/infrastructure/state"
	"github.com/seu-usuario/project-sentinel/internal/infrastructure/storage"
	"github.com/seu-usuario/project-sentinel/internal/platform/concurrency"
	"github.com/seu-usuario/project-sentinel/internal/registry"
	"github.com/seu-usuario/project-sentinel/internal/usecase"
	"go.uber.org/zap"
)

func main() {
	if err := run(); err != nil {
		log.Fatalf("sentinel failed: %v", err)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	appLogger, err := logger.New(cfg.LogLevel)
	if err != nil {
		return fmt.Errorf("initialize logger: %w", err)
	}
	defer func() {
		if err := appLogger.Sync(); err != nil {
			log.Printf("logger sync failed: %v", err)
		}
	}()

	encryptor, err := sentinelcrypto.NewAESGCMEncryptor([]byte(cfg.SessionEncryptionKey))
	if err != nil {
		return fmt.Errorf("initialize session encryptor: %w", err)
	}

	sessionStore := storage.NewFileSessionStore(cfg.SessionStorePath, encryptor)
	stateStore, err := state.OpenSQLiteStateStore(cfg.StateDBPath)
	if err != nil {
		return err
	}
	defer func() {
		if err := stateStore.Close(); err != nil {
			log.Printf("state store close failed: %v", err)
		}
	}()
	if err := stateStore.Migrate(context.Background()); err != nil {
		return err
	}
	stateStore.SetRotationStrategy(cfg.RotationStrategy)
	if err := backfillAccountStates(context.Background(), cfg.SessionStorePath, sessionStore, stateStore); err != nil {
		return err
	}

	accountLimiter := concurrency.NewKeyedLimiter(1)
	accountService := usecase.NewAccountService(sessionStore, stateStore, accountLimiter)

	// Model registry (simplified — no resource management, just model→upstream mapping)
	modelRegistry, err := registry.Load(cfg.ModelsConfigPath)
	if err != nil {
		return err
	}

	chatGPTAdapter, err := adapter.NewChatGPTAdapter(time.Duration(cfg.RequestTimeoutSeconds)*time.Second, cfg.DefaultReasoningEffort)
	if err != nil {
		return fmt.Errorf("initialize ChatGPT adapter: %w", err)
	}
	claudeAdapter := adapter.NewClaudeAdapter(time.Duration(cfg.RequestTimeoutSeconds) * time.Second)
	geminiAdapter := adapter.NewGeminiAdapter(time.Duration(cfg.RequestTimeoutSeconds) * time.Second)
	providerRegistry, err := adapter.NewProviderAdapterRegistry(chatGPTAdapter, claudeAdapter, geminiAdapter)
	if err != nil {
		return fmt.Errorf("initialize provider adapter registry: %w", err)
	}

	appLogger.Info("sentinel components initialized",
		zap.String("tls_profile", "Chrome_131"),
		zap.String("rotation_strategy", string(cfg.RotationStrategy)),
		zap.String("default_model", cfg.DefaultModel),
		zap.String("default_reasoning_effort", cfg.DefaultReasoningEffort),
		zap.String("upstream", "multi-provider"),
	)

	router := httpdelivery.NewRouter(httpdelivery.RouterDeps{
		AccountRegistrar:    accountService,
		Executor:            providerRegistry,
		ModelLister:         modelRegistry,
		ModelResolver:       modelRegistry,
		AccountAcquirer:     stateStore,
		SessionLoader:       sessionStore,
		LeaseReleaser:       stateStore,
		AccountLister:       stateStore,
		AccountStatusSetter: stateStore,
		RotationInspector:   stateStore,
		ForceModeManager:    stateStore,
		Logger:              appLogger,
		APIKey:              cfg.APIKey,
		DefaultModel:        cfg.DefaultModel,
		ReadyCheck: func(ctx context.Context) error {
			select {
			case <-ctx.Done():
				return ctx.Err()
			default:
			}

			if err := sessionStore.Ready(); err != nil {
				return err
			}
			return stateStore.Ready(ctx)
		},
	})

	server := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           router,
		ReadHeaderTimeout: 5 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)
	go func() {
		appLogger.Info("sentinel HTTP server starting", zap.String("addr", cfg.HTTPAddr))
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
			return
		}
		errCh <- nil
	}()

	select {
	case <-ctx.Done():
		appLogger.Info("shutdown signal received")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		if err := server.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("shutdown HTTP server: %w", err)
		}
		return nil
	case err := <-errCh:
		return err
	}
}

func backfillAccountStates(ctx context.Context, sessionStorePath string, sessionStore *storage.FileSessionStore, stateStore *state.SQLiteStateStore) error {
	entries, err := os.ReadDir(sessionStorePath)
	if err != nil {
		return fmt.Errorf("read session store for account state backfill: %w", err)
	}

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json.enc") {
			continue
		}

		accountID := strings.TrimSuffix(entry.Name(), ".json.enc")
		if accountID == "" {
			continue
		}
		provider := domain.ProviderChatGPT
		if session, err := sessionStore.Load(accountID); err == nil {
			if normalized, normalizeErr := domain.NormalizeProvider(session.Provider); normalizeErr == nil {
				provider = normalized
			}
		}

		if err := stateStore.UpsertAccountState(ctx, domain.AccountState{
			AccountID:     accountID,
			Provider:      provider,
			Status:        domain.AccountRoutingActive,
			DailyLimit:    100,
			PlanPriority:  0,
			MaxConcurrent: 1,
		}); err != nil {
			return fmt.Errorf("backfill account state %s: %w", accountID, err)
		}
	}

	return nil
}
