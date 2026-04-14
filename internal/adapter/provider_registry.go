package adapter

import (
	"context"
	"fmt"
	"strings"

	"github.com/seu-usuario/project-sentinel/internal/domain"
)

type ProviderAdapter interface {
	Provider() string
	Execute(
		ctx context.Context,
		requestID string,
		session *domain.Session,
		model domain.ResolvedModel,
		rawBody []byte,
		streamWriter func([]byte) error,
	) (*domain.ProviderResult, error)
}

type ProviderAdapterRegistry struct {
	adapters map[string]ProviderAdapter
}

func NewProviderAdapterRegistry(adapters ...ProviderAdapter) (*ProviderAdapterRegistry, error) {
	registry := &ProviderAdapterRegistry{
		adapters: make(map[string]ProviderAdapter, len(adapters)),
	}
	for _, adapter := range adapters {
		if err := registry.Register(adapter); err != nil {
			return nil, err
		}
	}
	return registry, nil
}

func (r *ProviderAdapterRegistry) Register(adapter ProviderAdapter) error {
	if adapter == nil {
		return fmt.Errorf("provider adapter is nil")
	}

	provider, err := domain.NormalizeProvider(adapter.Provider())
	if err != nil {
		return err
	}
	if _, exists := r.adapters[provider]; exists {
		return fmt.Errorf("provider adapter %q already registered", provider)
	}
	r.adapters[provider] = adapter
	return nil
}

func (r *ProviderAdapterRegistry) Execute(
	ctx context.Context,
	requestID string,
	session *domain.Session,
	model domain.ResolvedModel,
	rawBody []byte,
	streamWriter func([]byte) error,
) (*domain.ProviderResult, error) {
	if r == nil {
		return nil, fmt.Errorf("provider adapter registry is nil")
	}

	provider, err := domain.NormalizeProvider(model.Provider)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", domain.ErrInvalidData, err)
	}
	adapter, ok := r.adapters[provider]
	if !ok {
		return nil, fmt.Errorf("%w: no adapter for provider %q", domain.ErrInvalidData, provider)
	}
	if session != nil && strings.TrimSpace(session.Provider) != "" {
		sessionProvider, err := domain.NormalizeProvider(session.Provider)
		if err == nil && sessionProvider != provider {
			return nil, fmt.Errorf("%w: account provider %q does not match model provider %q", domain.ErrInvalidData, sessionProvider, provider)
		}
	}

	return adapter.Execute(ctx, requestID, session, model, rawBody, streamWriter)
}
