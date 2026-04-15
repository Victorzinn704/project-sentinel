package httpdelivery

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/seu-usuario/project-sentinel/internal/domain"
)

func TestAdminRoutesRequireAdminAPIKeyWhenConfigured(t *testing.T) {
	router := NewRouter(RouterDeps{
		AccountLister:       stubAdminAccountLister{},
		AccountStatusSetter: stubAdminAccountStatusSetter{},
		RotationInspector:   stubRotationInspector{},
		ForceModeManager:    stubForceModeManager{},
		APIKey:              "runtime-key",
		AdminAPIKey:         "admin-key",
	})

	req := httptest.NewRequest(http.MethodGet, "/admin/accounts", nil)
	req.Header.Set("X-API-Key", "runtime-key")
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected runtime key to be rejected for admin route, got %d", rr.Code)
	}

	req = httptest.NewRequest(http.MethodGet, "/admin/accounts", nil)
	req.Header.Set("X-API-Key", "admin-key")
	rr = httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected admin key to be accepted, got %d", rr.Code)
	}
}

type stubAdminAccountLister struct{}
func (stubAdminAccountLister) ListAccountStates(context.Context) ([]domain.AccountState, error) { return []domain.AccountState{}, nil }

type stubAdminAccountStatusSetter struct{}
func (stubAdminAccountStatusSetter) SetAccountRoutingStatus(context.Context, string, domain.AccountRoutingStatus) error { return nil }

type stubRotationInspector struct{}
func (stubRotationInspector) RotationStrategy() domain.RotationStrategy { return domain.RotationQuotaFirst }

type stubForceModeManager struct{}
func (stubForceModeManager) SetForceMode(context.Context, string) error { return nil }
func (stubForceModeManager) ClearForceMode(context.Context) error { return nil }
func (stubForceModeManager) GetForceModeState(context.Context) (domain.ForceModeState, error) { return domain.ForceModeState{}, nil }
