package httpdelivery

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/seu-usuario/project-sentinel/internal/domain"
)

type stubCodexPassthrough struct{}

func (stubCodexPassthrough) ProxyCodexRequest(
	ctx context.Context,
	requestID string,
	session *domain.Session,
	rawBody []byte,
	chunkWriter func([]byte) error,
) (*domain.ProviderResult, error) {
	panic("should not be called in unauthorized route test")
}

type stubAccountAcquirer struct{}

func (stubAccountAcquirer) AcquireLease(ctx context.Context, request domain.AccountLeaseRequest) (*domain.Lease, *domain.AccountState, error) {
	panic("should not be called in unauthorized route test")
}

type stubSessionLoader struct{}

func (stubSessionLoader) Load(accountID string) (*domain.Session, error) {
	panic("should not be called in unauthorized route test")
}

type stubLeaseReleaser struct{}

func (stubLeaseReleaser) ReleaseLease(ctx context.Context, lease domain.Lease) error {
	panic("should not be called in unauthorized route test")
}

func (stubLeaseReleaser) RecordSuccess(ctx context.Context, accountID string, latencyMs float64) error {
	panic("should not be called in unauthorized route test")
}

func (stubLeaseReleaser) RecordRateLimit(ctx context.Context, accountID string, retryAfterSeconds int) error {
	panic("should not be called in unauthorized route test")
}

func (stubLeaseReleaser) RecordAuthFailure(ctx context.Context, accountID string) error {
	panic("should not be called in unauthorized route test")
}

func (stubLeaseReleaser) RecordTransientFailure(ctx context.Context, accountID string) error {
	panic("should not be called in unauthorized route test")
}

func (stubLeaseReleaser) RecordQuotaSnapshot(ctx context.Context, accountID string, snapshot domain.AccountQuotaSnapshot) error {
	panic("should not be called in unauthorized route test")
}

func TestCodexPassthroughRoutesAcceptV1Aliases(t *testing.T) {
	router := NewRouter(RouterDeps{
		CodexPassthrough: stubCodexPassthrough{},
		AccountAcquirer:  stubAccountAcquirer{},
		SessionLoader:    stubSessionLoader{},
		LeaseReleaser:    stubLeaseReleaser{},
		APIKey:           "test-key",
	})

	paths := []string{
		"/backend-api/codex/responses",
		"/v1/backend-api/codex/responses",
		"/v1/v1/backend-api/codex/responses",
	}

	for _, path := range paths {
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader("{}"))
		req.Header.Set("Content-Type", "application/json")
		rr := httptest.NewRecorder()

		router.ServeHTTP(rr, req)

		if rr.Code != http.StatusUnauthorized {
			t.Fatalf("path %s: expected 401 unauthorized, got %d body=%s", path, rr.Code, rr.Body.String())
		}
	}
}
