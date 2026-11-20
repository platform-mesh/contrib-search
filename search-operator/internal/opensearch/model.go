/*
Copyright The Platform Mesh Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package opensearch

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/opensearch-project/opensearch-go/v4"
)

// modelStateDeployed is the ML Commons state of a model that can serve requests.
const modelStateDeployed = "DEPLOYED"

// ErrModelNotFound is returned when no ML model with the given name is registered.
var ErrModelNotFound = errors.New("ML model not found")

// ErrModelNotDeployed is returned when an ML model is registered but not deployed.
var ErrModelNotDeployed = errors.New("ML model not deployed")

// FindDeployedModelID returns the ID of the deployed ML Commons model registered
// under the exact name. If several deployed models share the name, the most
// recently created one wins.
func (c *Client) FindDeployedModelID(ctx context.Context, name string) (string, error) {
	body, err := json.Marshal(map[string]any{
		"size":    100,
		"_source": []string{"name", "model_state", "created_time"},
		"query": map[string]any{
			"bool": map[string]any{
				"must": []any{map[string]any{"match_phrase": map[string]any{"name": name}}},
				// Model content chunks share the model name; skip them.
				"must_not": []any{map[string]any{"exists": map[string]any{"field": "chunk_number"}}},
			},
		},
	})
	if err != nil {
		return "", fmt.Errorf("marshal ML model search: %w", err)
	}

	req, err := opensearch.BuildRequest(http.MethodPost, "/_plugins/_ml/models/_search", bytes.NewReader(body), nil,
		http.Header{"Content-Type": []string{"application/json"}})
	if err != nil {
		return "", fmt.Errorf("build ML model search request: %w", err)
	}
	resp, err := c.api.Client.Perform(req.WithContext(ctx))
	if err != nil {
		return "", fmt.Errorf("search ML models: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read ML model search response: %w", err)
	}
	// Before the first model is registered the ML model index does not exist.
	if resp.StatusCode == http.StatusNotFound {
		return "", fmt.Errorf("%w: %q", ErrModelNotFound, name)
	}
	if resp.StatusCode >= http.StatusBadRequest {
		return "", fmt.Errorf("search ML models: status %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}

	var result struct {
		Hits struct {
			Hits []struct {
				ID     string `json:"_id"`
				Source struct {
					Name        string `json:"name"`
					ModelState  string `json:"model_state"`
					CreatedTime int64  `json:"created_time"`
				} `json:"_source"`
			} `json:"hits"`
		} `json:"hits"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return "", fmt.Errorf("decode ML model search response: %w", err)
	}

	var (
		deployedID      string
		deployedCreated int64
		states          []string
	)
	for _, hit := range result.Hits.Hits {
		if hit.Source.Name != name {
			continue
		}
		states = append(states, hit.ID+"="+hit.Source.ModelState)
		if hit.Source.ModelState == modelStateDeployed && (deployedID == "" || hit.Source.CreatedTime > deployedCreated) {
			deployedID, deployedCreated = hit.ID, hit.Source.CreatedTime
		}
	}
	switch {
	case deployedID != "":
		return deployedID, nil
	case len(states) == 0:
		return "", fmt.Errorf("%w: %q", ErrModelNotFound, name)
	default:
		return "", fmt.Errorf("%w: %q (%s)", ErrModelNotDeployed, name, strings.Join(states, ", "))
	}
}

// ModelRef identifies an ML Commons model either by ID or by name.
type ModelRef struct {
	ID   string
	Name string
}

func (r ModelRef) isZero() bool {
	return strings.TrimSpace(r.ID) == "" && strings.TrimSpace(r.Name) == ""
}

// SemanticModels holds the ML model IDs used by semantic field mappings.
type SemanticModels struct {
	// ModelID generates embeddings from field values at ingest time and, unless
	// SearchModelID is set, from query text at search time.
	ModelID string
	// SearchModelID optionally generates embeddings from query text only, e.g. a
	// tokenizer paired with a document-only sparse encoder.
	SearchModelID string
}

type deployedModelFinder interface {
	FindDeployedModelID(ctx context.Context, name string) (string, error)
}

// SemanticModelResolver resolves the configured semantic models to model IDs.
// Models configured by name are looked up in OpenSearch on every call (only
// when an index with semantic fields is created), so the operator can start
// before the models are registered and picks up re-registered models.
type SemanticModelResolver struct {
	finder      deployedModelFinder
	model       ModelRef
	searchModel ModelRef
}

// NewSemanticModelResolver creates a resolver for the ingest model and the
// optional search model.
func NewSemanticModelResolver(finder deployedModelFinder, model, searchModel ModelRef) *SemanticModelResolver {
	return &SemanticModelResolver{finder: finder, model: model, searchModel: searchModel}
}

// Resolve returns the semantic model IDs. It returns zero SemanticModels when
// no model is configured.
func (r *SemanticModelResolver) Resolve(ctx context.Context) (SemanticModels, error) {
	modelID, err := r.resolveRef(ctx, r.model)
	if err != nil {
		return SemanticModels{}, fmt.Errorf("resolve semantic model: %w", err)
	}
	searchModelID, err := r.resolveRef(ctx, r.searchModel)
	if err != nil {
		return SemanticModels{}, fmt.Errorf("resolve semantic search model: %w", err)
	}

	return SemanticModels{ModelID: modelID, SearchModelID: searchModelID}, nil
}

func (r *SemanticModelResolver) resolveRef(ctx context.Context, ref ModelRef) (string, error) {
	if id := strings.TrimSpace(ref.ID); id != "" {
		return id, nil
	}
	name := strings.TrimSpace(ref.Name)
	if name == "" {
		return "", nil
	}
	if r.finder == nil {
		return "", fmt.Errorf("look up model %q: OpenSearch client not configured", name)
	}
	return r.finder.FindDeployedModelID(ctx, name)
}
