package registry

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/seu-usuario/project-sentinel/internal/domain"
)

// Config holds the model configuration loaded from models.json.
type Config struct {
	Models []ModelConfig `json:"models"`
}

// ModelConfig describes a logical model exposed to IDE clients.
// In the ChatGPT web architecture, there are no per-model "resources" (API keys).
// The resource pool (ChatGPT Plus accounts) is managed by the Scheduler/StateStore.
type ModelConfig struct {
	ID            string   `json:"id"`
	Provider      string   `json:"provider"`
	Upstream      string   `json:"upstream"`
	UpstreamModel string   `json:"upstream_model"`
	OwnedBy       string   `json:"owned_by"`
	Capabilities  []string `json:"capabilities"`
}

// Registry maps logical model IDs to upstream model slugs.
// Unlike the previous architecture, it does NOT manage resource pools or
// API keys — that responsibility belongs to the Scheduler + SQLiteStateStore.
type Registry struct {
	models map[string]ModelConfig
}

// Load reads and validates the model registry configuration file.
func Load(path string) (*Registry, error) {
	payload, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read model registry config: %w", err)
	}

	var cfg Config
	if err := json.Unmarshal(payload, &cfg); err != nil {
		return nil, fmt.Errorf("decode model registry config: %w", err)
	}

	registry := &Registry{
		models: make(map[string]ModelConfig, len(cfg.Models)),
	}

	for _, model := range cfg.Models {
		model.ID = strings.TrimSpace(model.ID)
		model.Provider = strings.TrimSpace(model.Provider)
		model.Upstream = strings.TrimSpace(model.Upstream)
		model.UpstreamModel = strings.TrimSpace(model.UpstreamModel)
		model.OwnedBy = strings.TrimSpace(model.OwnedBy)

		if model.ID == "" {
			return nil, fmt.Errorf("model id is required")
		}
		provider, err := domain.NormalizeProvider(model.Provider)
		if err != nil {
			return nil, err
		}
		model.Provider = provider
		if model.UpstreamModel == "" {
			model.UpstreamModel = model.Upstream
		}
		if model.UpstreamModel == "" {
			model.UpstreamModel = model.ID // default: same as logical ID
		}
		if model.OwnedBy == "" {
			model.OwnedBy = "project-sentinel"
		}

		if _, exists := registry.models[model.ID]; exists {
			return nil, fmt.Errorf("duplicate model id %q", model.ID)
		}

		registry.models[model.ID] = model
	}

	if len(registry.models) == 0 {
		return nil, fmt.Errorf("model registry config must define at least one model")
	}

	return registry, nil
}

// Models returns all registered models as domain ModelInfo structs.
func (r *Registry) Models() []domain.ModelInfo {
	models := make([]domain.ModelInfo, 0, len(r.models))
	for _, model := range r.models {
		models = append(models, domain.ModelInfo{
			ID:           model.ID,
			Provider:     model.Provider,
			Capabilities: append([]string(nil), model.Capabilities...),
			OwnedBy:      model.OwnedBy,
		})
	}
	sort.Slice(models, func(i, j int) bool {
		return models[i].ID < models[j].ID
	})
	return models
}

func (r *Registry) Resolve(requestedModel string) (domain.ResolvedModel, bool) {
	requestedModel = strings.TrimSpace(requestedModel)
	model, ok := r.models[requestedModel]
	if !ok {
		return domain.ResolvedModel{}, false
	}

	return domain.ResolvedModel{
		ID:            model.ID,
		Provider:      model.Provider,
		UpstreamModel: model.UpstreamModel,
		Capabilities:  append([]string(nil), model.Capabilities...),
		OwnedBy:       model.OwnedBy,
	}, true
}

// ResolveUpstreamModel takes a model ID requested by the IDE client and
// returns the upstream model slug that should be sent to the backend-api.
// Returns false if the model is not in the registry (caller may still
// pass the raw model to the backend-api as a best-effort attempt).
func (r *Registry) ResolveUpstreamModel(requestedModel string) (string, bool) {
	model, ok := r.Resolve(requestedModel)
	if !ok {
		return "", false
	}
	return model.UpstreamModel, true
}
