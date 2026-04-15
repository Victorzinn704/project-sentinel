package adapter

import (
	"bytes"
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"github.com/seu-usuario/project-sentinel/internal/domain"
)

func extractRuntimeQuotaSnapshotFromPayload(raw []byte, now time.Time) *domain.AccountQuotaSnapshot {
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil
	}

	var payload any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil
	}

	rateLimits, ok := findRuntimeRateLimits(payload)
	if !ok {
		return nil
	}

	snapshot, ok := parseRuntimeQuotaSnapshot(rateLimits, now)
	if !ok {
		return nil
	}
	return snapshot
}

func findRuntimeRateLimits(value any) (map[string]any, bool) {
	switch node := value.(type) {
	case map[string]any:
		if embedded, ok := node["rate_limits"].(map[string]any); ok {
			return embedded, true
		}
		if looksLikeRuntimeRateLimits(node) {
			return node, true
		}
		for _, child := range node {
			if found, ok := findRuntimeRateLimits(child); ok {
				return found, true
			}
		}
	case []any:
		for _, child := range node {
			if found, ok := findRuntimeRateLimits(child); ok {
				return found, true
			}
		}
	}
	return nil, false
}

func looksLikeRuntimeRateLimits(node map[string]any) bool {
	for _, key := range []string{"primary", "secondary", "primary_window", "secondary_window"} {
		if _, ok := node[key]; ok {
			return true
		}
	}
	return false
}

func parseRuntimeQuotaSnapshot(rateLimits map[string]any, now time.Time) (*domain.AccountQuotaSnapshot, bool) {
	primary := runtimeWindow(rateLimits, "primary", "primary_window")
	secondary := runtimeWindow(rateLimits, "secondary", "secondary_window")

	fiveHourWindow, weeklyWindow := classifyRuntimeWindows(primary, secondary)
	fiveHourRemaining := runtimeRemainingPct(fiveHourWindow)
	fiveHourResetAt := runtimeResetAt(fiveHourWindow, now)
	weeklyRemaining := runtimeRemainingPct(weeklyWindow)
	weeklyResetAt := runtimeResetAt(weeklyWindow, now)

	if fiveHourRemaining == nil && fiveHourResetAt == nil && weeklyRemaining == nil && weeklyResetAt == nil {
		return nil, false
	}

	snapshot := &domain.AccountQuotaSnapshot{
		Source:               "chatgpt_rate_limits",
		RefreshedAt:          now.UTC(),
		FiveHourRemainingPct: fiveHourRemaining,
		FiveHourResetAt:      fiveHourResetAt,
		WeeklyRemainingPct:   weeklyRemaining,
		WeeklyResetAt:        weeklyResetAt,
	}

	if blockedUntil := computeRuntimeBlockedUntil(rateLimits, fiveHourRemaining, fiveHourResetAt, weeklyRemaining, weeklyResetAt, now); blockedUntil != nil {
		snapshot.BlockedUntil = blockedUntil
	}

	return snapshot, true
}

func runtimeWindow(rateLimits map[string]any, keys ...string) map[string]any {
	for _, key := range keys {
		if node, ok := rateLimits[key].(map[string]any); ok {
			return node
		}
	}
	return nil
}

func classifyRuntimeWindows(primary map[string]any, secondary map[string]any) (map[string]any, map[string]any) {
	if runtimeWindowIsWeekly(primary) {
		return secondary, primary
	}
	if runtimeWindowIsWeekly(secondary) {
		return primary, secondary
	}
	return primary, secondary
}

func runtimeWindowIsWeekly(window map[string]any) bool {
	if window == nil {
		return false
	}
	if minutes, ok := numberFromAny(window["window_minutes"]); ok {
		return minutes >= 24*60
	}
	for _, key := range []string{"window_seconds", "limit_window_seconds"} {
		if seconds, ok := numberFromAny(window[key]); ok {
			return seconds >= 24*60*60
		}
	}
	return false
}

func runtimeRemainingPct(window map[string]any) *int {
	if window == nil {
		return nil
	}

	if remaining, ok := numberFromAny(window["remaining_percent"]); ok {
		value := clampPercent(int(remaining))
		return &value
	}
	if remaining, ok := numberFromAny(window["remaining_pct"]); ok {
		value := clampPercent(int(remaining))
		return &value
	}
	if used, ok := numberFromAny(window["used_percent"]); ok {
		value := clampPercent(int(100 - used))
		return &value
	}
	if used, ok := numberFromAny(window["used_percentage"]); ok {
		value := clampPercent(int(100 - used))
		return &value
	}
	return nil
}

func runtimeResetAt(window map[string]any, now time.Time) *time.Time {
	if window == nil {
		return nil
	}

	for _, key := range []string{"resets_at", "reset_at"} {
		if resetAt := timeFromAny(window[key]); resetAt != nil {
			return resetAt
		}
	}
	for _, key := range []string{"resets_in_seconds", "reset_after_seconds"} {
		if seconds, ok := numberFromAny(window[key]); ok && seconds > 0 {
			resetAt := now.Add(time.Duration(seconds) * time.Second).UTC()
			return &resetAt
		}
	}
	return nil
}

func computeRuntimeBlockedUntil(
	rateLimits map[string]any,
	fiveHourRemaining *int,
	fiveHourResetAt *time.Time,
	weeklyRemaining *int,
	weeklyResetAt *time.Time,
	now time.Time,
) *time.Time {
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

	limited := false
	if value, ok := boolFromAny(rateLimits["limit_reached"]); ok && value {
		limited = true
	}
	if value, ok := boolFromAny(rateLimits["allowed"]); ok && !value {
		limited = true
	}
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

func extractRuntimeQuotaSnapshotFromSSE(raw []byte, now time.Time) *domain.AccountQuotaSnapshot {
	lines := bytes.Split(raw, []byte("\n"))
	var latest *domain.AccountQuotaSnapshot

	for _, line := range lines {
		line = bytes.TrimSpace(line)
		if !bytes.HasPrefix(line, []byte("data:")) {
			continue
		}
		data := bytes.TrimSpace(bytes.TrimPrefix(line, []byte("data:")))
		if len(data) == 0 || bytes.Equal(data, []byte("[DONE]")) {
			continue
		}
		if snapshot := extractRuntimeQuotaSnapshotFromPayload(data, now); snapshot != nil {
			latest = snapshot
		}
	}

	return latest
}

func clampPercent(value int) int {
	if value < 0 {
		return 0
	}
	if value > 100 {
		return 100
	}
	return value
}

func numberFromAny(value any) (float64, bool) {
	switch raw := value.(type) {
	case float64:
		return raw, true
	case float32:
		return float64(raw), true
	case int:
		return float64(raw), true
	case int64:
		return float64(raw), true
	case int32:
		return float64(raw), true
	case json.Number:
		if parsed, err := raw.Float64(); err == nil {
			return parsed, true
		}
	case string:
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" {
			return 0, false
		}
		if parsed, err := strconv.ParseFloat(trimmed, 64); err == nil {
			return parsed, true
		}
	}
	return 0, false
}

func boolFromAny(value any) (bool, bool) {
	switch raw := value.(type) {
	case bool:
		return raw, true
	case string:
		trimmed := strings.TrimSpace(strings.ToLower(raw))
		switch trimmed {
		case "true":
			return true, true
		case "false":
			return false, true
		}
	}
	return false, false
}

func timeFromAny(value any) *time.Time {
	switch raw := value.(type) {
	case string:
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" {
			return nil
		}
		if parsed, err := time.Parse(time.RFC3339, trimmed); err == nil {
			timestamp := parsed.UTC()
			return &timestamp
		}
		if parsed, err := strconv.ParseFloat(trimmed, 64); err == nil {
			return unixTimestamp(parsed)
		}
	case float64:
		return unixTimestamp(raw)
	case int64:
		return unixTimestamp(float64(raw))
	case int:
		return unixTimestamp(float64(raw))
	case json.Number:
		if parsed, err := raw.Float64(); err == nil {
			return unixTimestamp(parsed)
		}
	}
	return nil
}

func unixTimestamp(raw float64) *time.Time {
	if raw <= 0 {
		return nil
	}
	if raw > 1e12 {
		raw = raw / 1000
	}
	timestamp := time.Unix(int64(raw), 0).UTC()
	return &timestamp
}
