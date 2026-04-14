package usecase

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/seu-usuario/project-sentinel/internal/domain"
	"github.com/seu-usuario/project-sentinel/internal/platform/concurrency"
)

// SessionStore persists encrypted account session credentials.
type SessionStore interface {
	Load(accountID string) (*domain.Session, error)
	Save(session *domain.Session) error
	Delete(accountID string) error
}

type AccountService struct {
	store        SessionStore
	stateManager domain.AccountStateManager
	limiter      *concurrency.KeyedLimiter
}

func NewAccountService(store SessionStore, stateManager domain.AccountStateManager, limiter *concurrency.KeyedLimiter) *AccountService {
	return &AccountService{
		store:        store,
		stateManager: stateManager,
		limiter:      limiter,
	}
}

// RegisterAccount creates an encrypted session file and a routing state entry
// for a ChatGPT Plus web account. The account's access token, cookies, and
// user-agent are stored in the session file (AES-GCM encrypted).
func (s *AccountService) RegisterAccount(ctx context.Context, account domain.Account) error {
	if err := validateAccount(account); err != nil {
		return err
	}

	release, err := s.limiter.Acquire(account.ID)
	if err != nil {
		return fmt.Errorf("acquire account registration lock: %w", err)
	}
	defer release()

	if _, err := s.store.Load(account.ID); err == nil {
		return domain.ErrAccountAlreadyExists
	} else if !errors.Is(err, domain.ErrNotFound) {
		return fmt.Errorf("check existing account session: %w", err)
	}

	now := time.Now().UTC()
	provider, err := domain.NormalizeProvider(account.Provider)
	if err != nil {
		return fmt.Errorf("%w: %v", domain.ErrInvalidData, err)
	}
	account.Provider = provider

	session := &domain.Session{
		AccountID:   account.ID,
		Provider:    account.Provider,
		AccessToken: account.AccessToken,
		UserAgent:   account.UserAgent,
		AuthParams:  account.AuthParams,
		LastUsedAt:  now,
		Version:     domain.CurrentSessionVersion,
	}

	if err := s.store.Save(session); err != nil {
		return fmt.Errorf("save initial account session: %w", err)
	}

	if s.stateManager != nil {
		if err := s.stateManager.UpsertAccountState(ctx, domain.AccountState{
			AccountID:     account.ID,
			Provider:      account.Provider,
			Status:        domain.AccountRoutingActive,
			DailyLimit:    100,
			PlanPriority:  0,
			MaxConcurrent: 1,
		}); err != nil {
			if cleanupErr := s.store.Delete(account.ID); cleanupErr != nil {
				return fmt.Errorf("save account routing state: %w; cleanup session failed: %v", err, cleanupErr)
			}
			return fmt.Errorf("save account routing state: %w", err)
		}
	}

	return nil
}

func validateAccount(account domain.Account) error {
	if strings.TrimSpace(account.ID) == "" {
		return fmt.Errorf("%w: account id is required", domain.ErrInvalidData)
	}
	if strings.TrimSpace(account.AccessToken) == "" {
		return fmt.Errorf("%w: access_token is required", domain.ErrInvalidData)
	}
	provider, err := domain.NormalizeProvider(account.Provider)
	if err != nil {
		return fmt.Errorf("%w: %v", domain.ErrInvalidData, err)
	}
	if provider == domain.ProviderChatGPT && strings.TrimSpace(account.UserAgent) == "" {
		return fmt.Errorf("%w: user_agent is required", domain.ErrInvalidData)
	}
	if strings.TrimSpace(account.Email) == "" {
		return nil
	}
	if strings.Contains(account.Email, "@") {
		return nil
	}

	return nil
}
