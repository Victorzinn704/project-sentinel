package adapter

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"strings"
	"time"

	fhttp "github.com/bogdanfinn/fhttp"
	"github.com/seu-usuario/project-sentinel/internal/domain"
)

const chatGPTUsageBaseURL = "https://chatgpt.com/backend-api"

type chatGPTUsageWindow struct {
	UsedPercent       *float64 `json:"used_percent"`
	LimitWindowSecs   *int64   `json:"limit_window_seconds"`
	ResetAfterSeconds *int64   `json:"reset_after_seconds"`
	ResetAt           *int64   `json:"reset_at"`
}

type chatGPTUsageRateLimit struct {
	Allowed         *bool               `json:"allowed"`
	LimitReached    *bool               `json:"limit_reached"`
	PrimaryWindow   *chatGPTUsageWindow `json:"primary_window"`
	SecondaryWindow *chatGPTUsageWindow `json:"secondary_window"`
}

type chatGPTAdditionalRateLimit struct {
	LimitName      string                 `json:"limit_name"`
	MeteredFeature string                 `json:"metered_feature"`
	RateLimit      *chatGPTUsageRateLimit `json:"rate_limit"`
}

type chatGPTUsagePayload struct {
	PlanType             string                       `json:"plan_type"`
	RateLimit            *chatGPTUsageRateLimit       `json:"rate_limit"`
	AdditionalRateLimits []chatGPTAdditionalRateLimit `json:"additional_rate_limits"`
}

func (a *ChatGPTAdapter) FetchQuotaSnapshot(ctx context.Context, session *domain.Session) (*domain.AccountQuotaSnapshot, error) {
	if session == nil {
		return nil, fmt.Errorf("%w: session is required", domain.ErrInvalidData)
	}

	req, err := fhttp.NewRequest(fhttp.MethodGet, resolveChatGPTUsageURL(session), nil)
	if err != nil {
		return nil, fmt.Errorf("create usage request: %w", err)
	}

	req.Header = fhttp.Header{
		"accept":     {"application/json"},
		"user-agent": {resolveUsageUserAgent(session)},
		"connection": {"Keep-Alive"},
	}
	if accountID := chatGPTAccountID(session); accountID != "" {
		req.Header.Set("chatgpt-account-id", accountID)
	}
	injectSessionCredentials(req, session)

	resp, err := a.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: usage request failed: %v", domain.ErrTransientUpstream, err)
	}
	defer resp.Body.Close()

	switch {
	case resp.StatusCode >= 200 && resp.StatusCode <= 299:
	case resp.StatusCode == 401 || resp.StatusCode == 403:
		return nil, domain.ErrAuthFailure
	default:
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, fmt.Errorf("usage API returned status %d: %s", resp.StatusCode, strings.TrimSpace(string(snippet)))
	}

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 32<<10))
	if err != nil {
		return nil, fmt.Errorf("read usage response: %w", err)
	}

	snapshot, err := parseChatGPTQuotaSnapshot(raw, time.Now().UTC())
	if err != nil {
		return nil, err
	}
	return snapshot, nil
}

func parseChatGPTQuotaSnapshot(raw []byte, now time.Time) (*domain.AccountQuotaSnapshot, error) {
	var payload chatGPTUsagePayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, fmt.Errorf("decode usage response: %w", err)
	}

	rateLimit := pickChatGPTUsageRateLimit(payload)
	if rateLimit == nil {
		return nil, fmt.Errorf("usage response contained no rate_limit data")
	}

	fiveHourWindow, weeklyWindow := classifyChatGPTUsageWindows(rateLimit)
	fiveHourRemaining := usageRemainingPct(fiveHourWindow)
	fiveHourResetAt := usageResetAt(fiveHourWindow, now)
	weeklyRemaining := usageRemainingPct(weeklyWindow)
	weeklyResetAt := usageResetAt(weeklyWindow, now)

	if fiveHourRemaining == nil && weeklyRemaining == nil && fiveHourResetAt == nil && weeklyResetAt == nil {
		return nil, fmt.Errorf("usage response contained no usable quota windows")
	}

	snapshot := &domain.AccountQuotaSnapshot{
		Source:               "chatgpt_wham_usage",
		RefreshedAt:          now.UTC(),
		FiveHourRemainingPct: fiveHourRemaining,
		FiveHourResetAt:      fiveHourResetAt,
		WeeklyRemainingPct:   weeklyRemaining,
		WeeklyResetAt:        weeklyResetAt,
	}
	if blockedUntil := computeUsageBlockedUntil(rateLimit, fiveHourRemaining, fiveHourResetAt, weeklyRemaining, weeklyResetAt, now); blockedUntil != nil {
		snapshot.BlockedUntil = blockedUntil
	}

	return snapshot, nil
}

func pickChatGPTUsageRateLimit(payload chatGPTUsagePayload) *chatGPTUsageRateLimit {
	if payload.RateLimit != nil {
		return payload.RateLimit
	}

	for _, entry := range payload.AdditionalRateLimits {
		feature := strings.ToLower(strings.TrimSpace(entry.MeteredFeature))
		limitName := strings.ToLower(strings.TrimSpace(entry.LimitName))
		if entry.RateLimit != nil && (feature == "codex" || limitName == "codex") {
			return entry.RateLimit
		}
	}
	for _, entry := range payload.AdditionalRateLimits {
		if entry.RateLimit != nil {
			return entry.RateLimit
		}
	}
	return nil
}

func classifyChatGPTUsageWindows(rateLimit *chatGPTUsageRateLimit) (*chatGPTUsageWindow, *chatGPTUsageWindow) {
	if rateLimit == nil {
		return nil, nil
	}

	primary := rateLimit.PrimaryWindow
	secondary := rateLimit.SecondaryWindow
	if usageWindowIsWeekly(primary) {
		return secondary, primary
	}
	if usageWindowIsWeekly(secondary) {
		return primary, secondary
	}
	return primary, secondary
}

func usageWindowIsWeekly(window *chatGPTUsageWindow) bool {
	if window == nil || window.LimitWindowSecs == nil {
		return false
	}
	return *window.LimitWindowSecs >= int64((24 * time.Hour).Seconds())
}

func usageRemainingPct(window *chatGPTUsageWindow) *int {
	if window == nil || window.UsedPercent == nil {
		return nil
	}

	value := int(math.Round(100 - *window.UsedPercent))
	if value < 0 {
		value = 0
	}
	if value > 100 {
		value = 100
	}
	return &value
}

func usageResetAt(window *chatGPTUsageWindow, now time.Time) *time.Time {
	if window == nil {
		return nil
	}
	if window.ResetAt != nil && *window.ResetAt > 0 {
		resetAt := time.Unix(*window.ResetAt, 0).UTC()
		return &resetAt
	}
	if window.ResetAfterSeconds != nil && *window.ResetAfterSeconds > 0 {
		resetAt := now.Add(time.Duration(*window.ResetAfterSeconds) * time.Second).UTC()
		return &resetAt
	}
	return nil
}

func computeUsageBlockedUntil(
	rateLimit *chatGPTUsageRateLimit,
	fiveHourRemaining *int,
	fiveHourResetAt *time.Time,
	weeklyRemaining *int,
	weeklyResetAt *time.Time,
	now time.Time,
) *time.Time {
	if rateLimit == nil {
		return nil
	}

	exhaustedResets := make([]time.Time, 0, 2)
	futureResets := make([]time.Time, 0, 2)
	for _, candidate := range []struct {
		remaining *int
		resetAt   *time.Time
	}{
		{remaining: fiveHourRemaining, resetAt: fiveHourResetAt},
		{remaining: weeklyRemaining, resetAt: weeklyResetAt},
	} {
		if candidate.resetAt == nil || !candidate.resetAt.After(now) {
			continue
		}
		futureResets = append(futureResets, candidate.resetAt.UTC())
		if candidate.remaining != nil && *candidate.remaining <= 0 {
			exhaustedResets = append(exhaustedResets, candidate.resetAt.UTC())
		}
	}

	if len(exhaustedResets) > 0 {
		latest := exhaustedResets[0]
		for _, resetAt := range exhaustedResets[1:] {
			if resetAt.After(latest) {
				latest = resetAt
			}
		}
		return &latest
	}

	limited := (rateLimit.LimitReached != nil && *rateLimit.LimitReached) || (rateLimit.Allowed != nil && !*rateLimit.Allowed)
	if limited && len(futureResets) > 0 {
		latest := futureResets[0]
		for _, resetAt := range futureResets[1:] {
			if resetAt.After(latest) {
				latest = resetAt
			}
		}
		return &latest
	}

	return nil
}

func resolveUsageUserAgent(session *domain.Session) string {
	if session != nil {
		if ua := strings.TrimSpace(session.UserAgent); ua != "" {
			return ua
		}
	}
	return "codex_cli_rs/0.101.0"
}

func resolveChatGPTUsageURL(session *domain.Session) string {
	base := strings.TrimSpace(resolveBaseURL(session, chatGPTUsageBaseURL))
	if base == "" {
		base = chatGPTUsageBaseURL
	}
	base = strings.TrimRight(base, "/")
	base = strings.TrimSuffix(base, "/responses/compact")
	base = strings.TrimSuffix(base, "/responses")
	if strings.HasSuffix(base, "/backend-api/codex") {
		base = strings.TrimSuffix(base, "/codex")
	}
	if idx := strings.Index(base, "/backend-api/codex/"); idx >= 0 {
		base = base[:idx] + "/backend-api"
	}
	if strings.HasSuffix(base, "/backend-api") {
		return base + "/wham/usage"
	}
	if idx := strings.Index(base, "/backend-api/"); idx >= 0 {
		base = base[:idx+len("/backend-api")]
		return strings.TrimRight(base, "/") + "/wham/usage"
	}
	return strings.TrimRight(base, "/") + "/backend-api/wham/usage"
}
