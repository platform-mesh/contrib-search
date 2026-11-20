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
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func newTestClient(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()

	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	client, err := NewClient(Config{URL: server.URL, Username: "hyperspace", Password: "secret"})
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	return client
}

func TestFindDeployedModelID(t *testing.T) {
	const name = "amazon/neural-sparse/opensearch-neural-sparse-encoding-doc-v3-distill"

	tests := []struct {
		name    string
		status  int
		body    string
		want    string
		wantErr error
	}{
		{
			name:   "newest deployed model with exact name wins",
			status: http.StatusOK,
			body: `{"hits":{"hits":[
				{"_id":"old","_source":{"name":"` + name + `","model_state":"DEPLOYED","created_time":1}},
				{"_id":"new","_source":{"name":"` + name + `","model_state":"DEPLOYED","created_time":2}},
				{"_id":"failed","_source":{"name":"` + name + `","model_state":"DEPLOY_FAILED","created_time":3}},
				{"_id":"other","_source":{"name":"` + name + `-v2","model_state":"DEPLOYED","created_time":4}}
			]}}`,
			want: "new",
		},
		{
			name:    "registered but not deployed",
			status:  http.StatusOK,
			body:    `{"hits":{"hits":[{"_id":"m1","_source":{"name":"` + name + `","model_state":"DEPLOYING","created_time":1}}]}}`,
			wantErr: ErrModelNotDeployed,
		},
		{
			name:    "no model with that name",
			status:  http.StatusOK,
			body:    `{"hits":{"hits":[]}}`,
			wantErr: ErrModelNotFound,
		},
		{
			name:    "ML model index missing",
			status:  http.StatusNotFound,
			body:    `{"error":{"type":"index_not_found_exception"},"status":404}`,
			wantErr: ErrModelNotFound,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost || r.URL.Path != "/_plugins/_ml/models/_search" {
					t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
				}
				if user, pass, ok := r.BasicAuth(); !ok || user != "hyperspace" || pass != "secret" {
					t.Errorf("request is not authenticated with the client credentials")
				}
				reqBody, _ := io.ReadAll(r.Body)
				if !strings.Contains(string(reqBody), `"match_phrase":{"name":"`+name+`"}`) {
					t.Errorf("request body %s does not match on the model name", reqBody)
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tt.status)
				_, _ = io.WriteString(w, tt.body)
			})

			got, err := client.FindDeployedModelID(context.Background(), name)
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("FindDeployedModelID() error = %v, want %v", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("FindDeployedModelID() error = %v", err)
			}
			if got != tt.want {
				t.Fatalf("FindDeployedModelID() = %q, want %q", got, tt.want)
			}
		})
	}
}

type fakeModelFinder struct {
	ids   map[string]string
	err   error
	calls int
}

func (f *fakeModelFinder) FindDeployedModelID(_ context.Context, name string) (string, error) {
	f.calls++
	if f.err != nil {
		return "", f.err
	}
	id, ok := f.ids[name]
	if !ok {
		return "", ErrModelNotFound
	}
	return id, nil
}

func TestSemanticModelResolver(t *testing.T) {
	finder := &fakeModelFinder{ids: map[string]string{"doc": "doc-id", "tok": "tok-id"}}

	tests := []struct {
		name        string
		model       ModelRef
		searchModel ModelRef
		want        SemanticModels
	}{
		{name: "nothing configured", want: SemanticModels{}},
		{name: "IDs are used as is", model: ModelRef{ID: "a"}, searchModel: ModelRef{ID: "b"}, want: SemanticModels{ModelID: "a", SearchModelID: "b"}},
		{name: "names are looked up", model: ModelRef{Name: "doc"}, searchModel: ModelRef{Name: "tok"}, want: SemanticModels{ModelID: "doc-id", SearchModelID: "tok-id"}},
		{name: "model without search model", model: ModelRef{Name: "doc"}, want: SemanticModels{ModelID: "doc-id"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := NewSemanticModelResolver(finder, tt.model, tt.searchModel).Resolve(context.Background())
			if err != nil {
				t.Fatalf("Resolve() error = %v", err)
			}
			if got != tt.want {
				t.Fatalf("Resolve() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestSemanticModelResolverLooksUpOnEveryCall(t *testing.T) {
	finder := &fakeModelFinder{err: ErrModelNotDeployed}
	resolver := NewSemanticModelResolver(finder, ModelRef{Name: "doc"}, ModelRef{})

	if _, err := resolver.Resolve(context.Background()); !errors.Is(err, ErrModelNotDeployed) {
		t.Fatalf("Resolve() error = %v, want %v", err, ErrModelNotDeployed)
	}

	// Deployed later, then re-registered under a new ID.
	finder.err = nil
	for _, id := range []string{"doc-id", "doc-id-2"} {
		finder.ids = map[string]string{"doc": id}
		got, err := resolver.Resolve(context.Background())
		if err != nil {
			t.Fatalf("Resolve() error = %v", err)
		}
		if got.ModelID != id {
			t.Fatalf("Resolve().ModelID = %q, want %q", got.ModelID, id)
		}
	}
}
