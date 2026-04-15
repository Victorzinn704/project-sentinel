package usecase

import (
	"context"
	"fmt"
	"time"

	"github.com/seu-usuario/project-sentinel/internal/domain"
	"go.uber.org/zap"
)

type quotaSnapshotFetcher interface {
	FetchQuotaSnapshot(ctx context.Context, session *domain.Session) (*domain.AccountQuotaSnapshot, error)
}

type quotaStateStore interface {
	ListAccountStates(ctx context.Context) ([]domain.AccountState, error)
	RecordQuotaSnapshot(ctx context.Context, accountID string, snapshot domain.AccountQuotaSnapshot) error
}

type quotaRefreshLogger interface {
	Info(message string, fields ...zap.Field)
	Warn(message string, fields ...zap.Field)
}

type AccountQuotaRefresher struct {
	sessions SessionStore
	state    quotaStateStore
	fetcher  quotaSnapshotFetcher
	logger   quotaRefreshLogger
}

func NewAccountQuotaRefresher(
	sessions SessionStore,
	state quotaStateStore,
	fetcher quotaSnapshotFetcher,
	logger quotaRefreshLogger,
) *AccountQuotaRefresher {
	return &AccountQuotaRefresher{
		sessions: sessions,
		state:    state,
		fetcher:  fetcher,
		logger:   logger,
	}
}

func (r *AccountQuotaRefresher) RefreshAll(ctx context.Context) error {
	states, err := r.state.ListAccountStates(ctx)
	if err != nil {
		return fmt.Errorf("list account states for quota refresh: %w", err)
	}

	refreshed := 0
	failures := 0
	for _, account := range states {
		if account.Provider != domain.ProviderChatGPT {
			continue
		}
		if err := ctx.Err(); err != nil {
			return err
		}

		session, err := r.sessions.Load(account.AccountID)
		if err != nil {
			failures++
			r.logWarn("load account session for quota refresh failed", account.AccountID, err)
			continue
		}

		snapshot, err := r.fetcher.FetchQuotaSnapshot(ctx, session)
		if err != nil {
			failures++
			r.logWarn("fetch account quota snapshot failed", account.AccountID, err)
			continue
		}

		if err := r.state.RecordQuotaSnapshot(ctx, account.AccountID, *snapshot); err != nil {
			failures++
			r.logWarn("persist account quota snapshot failed", account.AccountID, err)
			continue
		}
		refreshed++
	}

	if r.logger != nil {
		r.logger.Info(
			"account quota refresh completed",
			zap.Int("refreshed_accounts", refreshed),
			zap.Int("failed_accounts", failures),
		)
	}

	return nil
}

func (r *AccountQuotaRefresher) Run(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		return
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := r.RefreshAll(ctx); err != nil && r.logger != nil && ctx.Err() == nil {
				r.logger.Warn("scheduled account quota refresh failed", zap.String("error", err.Error()))
			}
		}
	}
}

func (r *AccountQuotaRefresher) logWarn(message string, accountID string, err error) {
	if r.logger == nil {
		return
	}
	r.logger.Warn(
		message,
		zap.String("account_id", accountID),
		zap.String("error", err.Error()),
	)
}
