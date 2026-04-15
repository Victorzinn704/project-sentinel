package adapter

import (
	"testing"
	"time"

	"github.com/seu-usuario/project-sentinel/internal/domain"
)

func TestParseChatGPTQuotaSnapshot(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 4, 14, 12, 0, 0, 0, time.UTC)
	raw := []byte(`{
		"plan_type":"pro",
		"rate_limit":{
			"allowed":true,
			"limit_reached":false,
			"primary_window":{
				"used_percent":92,
				"limit_window_seconds":18000,
				"reset_after_seconds":600
			},
			"secondary_window":{
				"used_percent":60,
				"limit_window_seconds":604800,
				"reset_at":1776200400
			}
		}
	}`)

	snapshot, err := parseChatGPTQuotaSnapshot(raw, now)
	if err != nil {
		t.Fatalf("parse snapshot: %v", err)
	}
	if snapshot.Source != "chatgpt_wham_usage" {
		t.Fatalf("source = %q, want chatgpt_wham_usage", snapshot.Source)
	}
	if snapshot.FiveHourRemainingPct == nil || *snapshot.FiveHourRemainingPct != 8 {
		t.Fatalf("fiveHourRemaining = %v, want 8", snapshot.FiveHourRemainingPct)
	}
	if snapshot.WeeklyRemainingPct == nil || *snapshot.WeeklyRemainingPct != 40 {
		t.Fatalf("weeklyRemaining = %v, want 40", snapshot.WeeklyRemainingPct)
	}
	if snapshot.FiveHourResetAt == nil || !snapshot.FiveHourResetAt.Equal(now.Add(10*time.Minute)) {
		t.Fatalf("fiveHourResetAt = %v, want %v", snapshot.FiveHourResetAt, now.Add(10*time.Minute))
	}
	if snapshot.BlockedUntil != nil {
		t.Fatalf("blockedUntil = %v, want nil because remaining quota still exists", snapshot.BlockedUntil)
	}
}

func TestParseChatGPTQuotaSnapshotBlockedWindow(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 4, 14, 12, 0, 0, 0, time.UTC)
	raw := []byte(`{
		"additional_rate_limits":[
			{
				"metered_feature":"codex",
				"rate_limit":{
					"allowed":false,
					"limit_reached":true,
					"primary_window":{
						"used_percent":100,
						"limit_window_seconds":18000,
						"reset_after_seconds":120
					},
					"secondary_window":{
						"used_percent":100,
						"limit_window_seconds":604800,
						"reset_after_seconds":3600
					}
				}
			}
		]
	}`)

	snapshot, err := parseChatGPTQuotaSnapshot(raw, now)
	if err != nil {
		t.Fatalf("parse snapshot: %v", err)
	}
	if snapshot.BlockedUntil == nil || !snapshot.BlockedUntil.Equal(now.Add(time.Hour)) {
		t.Fatalf("blockedUntil = %v, want %v", snapshot.BlockedUntil, now.Add(time.Hour))
	}
}

func TestResolveChatGPTUsageURL(t *testing.T) {
	t.Parallel()

	session := &domain.Session{
		AuthParams: map[string]string{
			"base_url": "https://chatgpt.com/backend-api/codex/responses",
		},
	}

	got := resolveChatGPTUsageURL(session)
	want := "https://chatgpt.com/backend-api/wham/usage"
	if got != want {
		t.Fatalf("resolveChatGPTUsageURL() = %q, want %q", got, want)
	}
}
