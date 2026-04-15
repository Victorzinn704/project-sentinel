package adapter

import (
	"testing"
	"time"
)

func TestExtractRuntimeQuotaSnapshotFromPayload(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 4, 14, 12, 0, 0, 0, time.UTC)
	raw := []byte(`{
		"type":"response.completed",
		"response":{
			"id":"resp_123",
			"rate_limits":{
				"allowed":true,
				"limit_reached":false,
				"primary":{
					"used_percent":21,
					"window_minutes":300,
					"resets_in_seconds":90
				},
				"secondary":{
					"used_percent":65,
					"window_minutes":10080,
					"resets_at":1776211200
				}
			}
		}
	}`)

	snapshot := extractRuntimeQuotaSnapshotFromPayload(raw, now)
	if snapshot == nil {
		t.Fatal("snapshot = nil, want parsed quota snapshot")
	}
	if snapshot.Source != "chatgpt_rate_limits" {
		t.Fatalf("source = %q, want chatgpt_rate_limits", snapshot.Source)
	}
	if snapshot.FiveHourRemainingPct == nil || *snapshot.FiveHourRemainingPct != 79 {
		t.Fatalf("fiveHourRemainingPct = %v, want 79", snapshot.FiveHourRemainingPct)
	}
	if snapshot.WeeklyRemainingPct == nil || *snapshot.WeeklyRemainingPct != 35 {
		t.Fatalf("weeklyRemainingPct = %v, want 35", snapshot.WeeklyRemainingPct)
	}
	if snapshot.FiveHourResetAt == nil || !snapshot.FiveHourResetAt.Equal(now.Add(90*time.Second)) {
		t.Fatalf("fiveHourResetAt = %v, want %v", snapshot.FiveHourResetAt, now.Add(90*time.Second))
	}
}

func TestExtractRuntimeQuotaSnapshotFromSSEUsesLatestRateLimits(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 4, 14, 12, 0, 0, 0, time.UTC)
	raw := []byte("data: {\"type\":\"response.output_text.delta\",\"delta\":\"oi\"}\n\ndata: {\"type\":\"response.completed\",\"response\":{\"rate_limits\":{\"allowed\":false,\"limit_reached\":true,\"primary\":{\"used_percent\":100,\"window_minutes\":300,\"resets_in_seconds\":120},\"secondary\":{\"used_percent\":88,\"window_minutes\":10080,\"resets_in_seconds\":86400}}}}\n\ndata: [DONE]\n\n")

	snapshot := extractRuntimeQuotaSnapshotFromSSE(raw, now)
	if snapshot == nil {
		t.Fatal("snapshot = nil, want quota snapshot")
	}
	if snapshot.BlockedUntil == nil || !snapshot.BlockedUntil.Equal(now.Add(120*time.Second)) {
		t.Fatalf("blockedUntil = %v, want %v", snapshot.BlockedUntil, now.Add(120*time.Second))
	}
	if snapshot.FiveHourRemainingPct == nil || *snapshot.FiveHourRemainingPct != 0 {
		t.Fatalf("fiveHourRemainingPct = %v, want 0", snapshot.FiveHourRemainingPct)
	}
}
