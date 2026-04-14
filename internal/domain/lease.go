package domain

import "time"

type Lease struct {
	LeaseID    string
	ResourceID string
	AccountID  string
	SessionID  string
	RequestID  string
	ExpiresAt  time.Time
	CreatedAt  time.Time
}
