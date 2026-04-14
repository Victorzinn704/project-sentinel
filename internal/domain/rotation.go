package domain

import (
	"fmt"
	"strings"
)

// RotationStrategy selects which eligible account wins the next lease.
type RotationStrategy string

const (
	// RotationLeastUsed prefers accounts with the lowest daily usage count,
	// then oldest last_used_at, then lowest EWMA latency. Best default for
	// spreading load evenly against daily quotas.
	RotationLeastUsed RotationStrategy = "least_used"

	// RotationRoundRobin prefers the account idle the longest (oldest
	// last_used_at). Ignores usage counts — useful when every account has
	// effectively unlimited quota.
	RotationRoundRobin RotationStrategy = "round_robin"

	// RotationRandom picks any eligible account uniformly at random. Good
	// for obscuring request patterns from upstream detection heuristics.
	RotationRandom RotationStrategy = "random"

	// RotationWeighted prefers highest plan_priority first, falling back to
	// oldest last_used_at within the same tier. Use when accounts have
	// heterogeneous plans (e.g. Plus vs Pro).
	RotationWeighted RotationStrategy = "weighted_round_robin"
)

func ParseRotationStrategy(raw string) (RotationStrategy, error) {
	normalized := strings.ToLower(strings.TrimSpace(raw))
	switch normalized {
	case "", string(RotationLeastUsed):
		return RotationLeastUsed, nil
	case string(RotationRoundRobin), "round-robin", "roundrobin":
		return RotationRoundRobin, nil
	case string(RotationRandom):
		return RotationRandom, nil
	case string(RotationWeighted), "weighted", "weighted-round-robin":
		return RotationWeighted, nil
	default:
		return "", fmt.Errorf("unknown rotation strategy %q", raw)
	}
}
