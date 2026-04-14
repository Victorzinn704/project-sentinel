package httpdelivery

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/seu-usuario/project-sentinel/internal/domain"
	"go.uber.org/zap"
)

// AccountLister lists every account's routing state.
type AccountLister interface {
	ListAccountStates(ctx context.Context) ([]domain.AccountState, error)
}

// AccountStatusSetter flips an account's routing status (enable/disable).
type AccountStatusSetter interface {
	SetAccountRoutingStatus(ctx context.Context, accountID string, status domain.AccountRoutingStatus) error
}

// RotationInspector exposes the active rotation strategy (read-only).
type RotationInspector interface {
	RotationStrategy() domain.RotationStrategy
}

type ForceModeManager interface {
	GetForceModeState(ctx context.Context) (domain.ForceModeState, error)
	SetForceMode(ctx context.Context, accountID string) error
	ClearForceMode(ctx context.Context) error
}

// AdminAccountDTO is a JSON-safe projection of domain.AccountState.
type AdminAccountDTO struct {
	AccountID       string  `json:"account_id"`
	Provider        string  `json:"provider"`
	Status          string  `json:"status"`
	LastUsedAt      *string `json:"last_used_at,omitempty"`
	DailyUsageCount int     `json:"daily_usage_count"`
	DailyLimit      int     `json:"daily_limit"`
	UsageDate       string  `json:"usage_date"`
	CooldownUntil   *string `json:"cooldown_until,omitempty"`
	LatencyEWMAMs   float64 `json:"latency_ewma_ms"`
	ErrorCount      int     `json:"error_count"`
	PlanPriority    int     `json:"plan_priority"`
	ActiveLeases    int     `json:"active_leases"`
	MaxConcurrent   int     `json:"max_concurrent"`
	RetryAfterUntil *string `json:"retry_after_until,omitempty"`
}

type AdminAccountsResponse struct {
	Count    int               `json:"count"`
	Accounts []AdminAccountDTO `json:"accounts"`
}

type AdminStateResponse struct {
	RotationStrategy string  `json:"rotation_strategy"`
	ForceModeActive  bool    `json:"force_mode_active"`
	ForcedAccountID  string  `json:"forced_account_id,omitempty"`
	ForceUpdatedAt   *string `json:"force_updated_at,omitempty"`
	AccountCount     int     `json:"account_count"`
	ActiveAccounts   int     `json:"active_accounts"`
	CooldownAccounts int     `json:"cooldown_accounts"`
	DisabledAccounts int     `json:"disabled_accounts"`
	ActiveLeases     int     `json:"active_leases"`
}

type AdminForceRequest struct {
	AccountID string `json:"account_id,omitempty"`
	Enabled   *bool  `json:"enabled,omitempty"`
}

type AdminForceResponse struct {
	Active    bool    `json:"active"`
	AccountID string  `json:"account_id,omitempty"`
	UpdatedAt *string `json:"updated_at,omitempty"`
}

// GetAdminAccountsHandler returns a snapshot of every account's routing state.
// Protected by the same API key middleware as the rest of the router.
func GetAdminAccountsHandler(lister AccountLister) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		states, err := lister.ListAccountStates(r.Context())
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: err.Error()})
			return
		}

		accounts := make([]AdminAccountDTO, 0, len(states))
		for _, state := range states {
			accounts = append(accounts, toAdminAccountDTO(state))
		}

		writeJSON(w, http.StatusOK, AdminAccountsResponse{
			Count:    len(accounts),
			Accounts: accounts,
		})
	}
}

// GetAdminStateHandler returns a global snapshot of the routing subsystem:
// active strategy + aggregate account counts by status. Cheap to hit from
// dashboards.
func GetAdminStateHandler(lister AccountLister, inspector RotationInspector, forceMode ForceModeManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		states, err := lister.ListAccountStates(r.Context())
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: err.Error()})
			return
		}

		response := AdminStateResponse{
			AccountCount: len(states),
		}
		if inspector != nil {
			response.RotationStrategy = string(inspector.RotationStrategy())
		}
		if forceMode != nil {
			forceState, err := forceMode.GetForceModeState(r.Context())
			if err != nil {
				writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: err.Error()})
				return
			}
			response.ForceModeActive = forceState.Active
			response.ForcedAccountID = forceState.AccountID
			response.ForceUpdatedAt = formatOptionalTime(&forceState.UpdatedAt)
		}
		for _, state := range states {
			response.ActiveLeases += state.ActiveLeases
			switch state.Status {
			case domain.AccountRoutingActive:
				response.ActiveAccounts++
			case domain.AccountRoutingCooldown:
				response.CooldownAccounts++
			case domain.AccountRoutingDisabled, domain.AccountRoutingAttentionRequired:
				response.DisabledAccounts++
			}
		}

		writeJSON(w, http.StatusOK, response)
	}
}

func PostAdminForceModeHandler(manager ForceModeManager, logger HandlerLogger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if manager == nil {
			writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: "force mode manager is not configured"})
			return
		}

		var payload AdminForceRequest
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&payload); err != nil {
			writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "invalid JSON payload"})
			return
		}

		accountID := strings.TrimSpace(payload.AccountID)
		clearForce := payload.Enabled != nil && !*payload.Enabled
		if accountID == "" && !clearForce {
			clearForce = true
		}

		var err error
		if clearForce {
			err = manager.ClearForceMode(r.Context())
		} else {
			err = manager.SetForceMode(r.Context(), accountID)
		}
		if err != nil {
			switch {
			case errors.Is(err, domain.ErrNotFound):
				writeJSON(w, http.StatusNotFound, ErrorResponse{Error: "account not found"})
			case errors.Is(err, domain.ErrNoEligibleAccounts):
				writeJSON(w, http.StatusConflict, ErrorResponse{Error: "account cannot be forced"})
			default:
				writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: err.Error()})
			}
			return
		}

		state, err := manager.GetForceModeState(r.Context())
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: err.Error()})
			return
		}
		if logger != nil {
			logger.Info(
				"force mode updated",
				zap.Bool("active", state.Active),
				zap.String("account_id", state.AccountID),
			)
		}

		writeJSON(w, http.StatusOK, AdminForceResponse{
			Active:    state.Active,
			AccountID: state.AccountID,
			UpdatedAt: formatOptionalTime(&state.UpdatedAt),
		})
	}
}

// PostAdminAccountStatusHandler handles both enable and disable operations.
// The target status is bound at route-registration time so the path-param
// matcher can point both /enable and /disable here.
func PostAdminAccountStatusHandler(setter AccountStatusSetter, status domain.AccountRoutingStatus, logger HandlerLogger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		accountID := strings.TrimSpace(r.PathValue("id"))
		if accountID == "" {
			writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "account id is required"})
			return
		}

		if err := setter.SetAccountRoutingStatus(r.Context(), accountID, status); err != nil {
			if errors.Is(err, domain.ErrNotFound) {
				writeJSON(w, http.StatusNotFound, ErrorResponse{Error: "account not found"})
				return
			}
			writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: err.Error()})
			return
		}

		if logger != nil {
			logger.Info(
				"account routing status updated",
				zap.String("account_id", accountID),
				zap.String("status", string(status)),
			)
		}

		writeJSON(w, http.StatusOK, AccountResponse{
			Success:   true,
			Message:   "account " + string(status),
			AccountID: accountID,
		})
	}
}

func toAdminAccountDTO(state domain.AccountState) AdminAccountDTO {
	return AdminAccountDTO{
		AccountID:       state.AccountID,
		Provider:        state.Provider,
		Status:          string(state.Status),
		LastUsedAt:      formatOptionalTime(state.LastUsedAt),
		DailyUsageCount: state.DailyUsageCount,
		DailyLimit:      state.DailyLimit,
		UsageDate:       state.UsageDate,
		CooldownUntil:   formatOptionalTime(state.CooldownUntil),
		LatencyEWMAMs:   state.LatencyEWMAMs,
		ErrorCount:      state.ErrorCount,
		PlanPriority:    state.PlanPriority,
		ActiveLeases:    state.ActiveLeases,
		MaxConcurrent:   state.MaxConcurrent,
		RetryAfterUntil: formatOptionalTime(state.RetryAfterUntil),
	}
}

func formatOptionalTime(value *time.Time) *string {
	if value == nil {
		return nil
	}

	formatted := value.UTC().Format(time.RFC3339Nano)
	return &formatted
}
