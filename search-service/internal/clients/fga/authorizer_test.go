package fga

import (
	"reflect"
	"testing"

	"github.com/platform-mesh/search/internal/service/search"
)

func TestBuildBatchCheckItemResourceObjectFormat(t *testing.T) {
	hit := search.OpenSearchHit{Source: map[string]interface{}{
		"kind":            "Component",
		"name":            "my-component",
		"namespace":       "dev",
		"api_group":       "core.platform-mesh.io",
		"organization_id": "orgcluster1",
		"account_id":      "acccluster1",
		"account_name":    "account-a",
	}}

	item, missing := buildBatchCheckItem("alice@example.com", "get", 0, hit)
	if missing {
		t.Fatalf("expected context to be valid")
	}
	if item.TupleKey.Relation != "get" {
		t.Fatalf("unexpected relation: %s", item.TupleKey.Relation)
	}
	expected := "core_platform-mesh_io_component:acccluster1/dev/my-component"
	if item.TupleKey.Object != expected {
		t.Fatalf("unexpected object: %s", item.TupleKey.Object)
	}
	if len(item.ContextualTuples.TupleKeys) == 0 {
		t.Fatalf("expected contextual tuples")
	}
}

func TestBuildBatchCheckItemDropsMissingAuthContext(t *testing.T) {
	hit := search.OpenSearchHit{Source: map[string]interface{}{
		"kind":            "Component",
		"name":            "my-component",
		"namespace":       "dev",
		"api_group":       "core.platform-mesh.io",
		"organization_id": "orgcluster1",
		// account_name intentionally missing for namespaced resources
	}}

	_, missing := buildBatchCheckItem("alice@example.com", "get", 0, hit)
	if !missing {
		t.Fatalf("expected missing auth context")
	}
}

func TestChunkRanges(t *testing.T) {
	tests := []struct {
		name      string
		total     int
		chunkSize int
		want      [][2]int
	}{
		{
			name:      "empty",
			total:     0,
			chunkSize: 100,
			want:      nil,
		},
		{
			name:      "single chunk exact",
			total:     100,
			chunkSize: 100,
			want:      [][2]int{{0, 100}},
		},
		{
			name:      "single chunk partial",
			total:     50,
			chunkSize: 100,
			want:      [][2]int{{0, 50}},
		},
		{
			name:      "multiple chunks",
			total:     250,
			chunkSize: 100,
			want:      [][2]int{{0, 100}, {100, 200}, {200, 250}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := chunkRanges(tt.total, tt.chunkSize)
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("chunkRanges(%d, %d) = %#v, want %#v", tt.total, tt.chunkSize, got, tt.want)
			}
		})
	}
}

func TestBuildAuthorizationContextUsesClusterNameAsResourceClusterID(t *testing.T) {
	source := map[string]interface{}{
		"kind":            "Component",
		"name":            "my-component",
		"namespace":       "dev",
		"api_group":       "core.platform-mesh.io",
		"cluster_name":    "gencluster99",  // GeneratedClusterId of the resource workspace
		"organization_id": "orgcluster1",
		"account_id":      "acccluster1",   // OriginClusterId (parent workspace)
		"account_name":    "account-a",
	}

	ctx, ok := buildAuthorizationContext(source)
	if !ok {
		t.Fatalf("expected valid authorization context")
	}

	// Resource object must use cluster_name (gencluster99), not account_id
	expectedObject := "core_platform-mesh_io_component:gencluster99/dev/my-component"
	if ctx.object != expectedObject {
		t.Fatalf("expected object %q, got %q", expectedObject, ctx.object)
	}

	// Namespace tuple must also use cluster_name for the namespace object
	if len(ctx.contextualTuples) == 0 {
		t.Fatalf("expected contextual tuples")
	}
	nsTuple := ctx.contextualTuples[0]
	expectedNS := "core_namespace:gencluster99/dev"
	if nsTuple.Object != expectedNS {
		t.Fatalf("expected namespace object %q, got %q", expectedNS, nsTuple.Object)
	}
	// Account tuple must use account_id (acccluster1), not cluster_name
	expectedAccount := "core_platform-mesh_io_account:acccluster1/account-a"
	if nsTuple.User != expectedAccount {
		t.Fatalf("expected account user %q, got %q", expectedAccount, nsTuple.User)
	}
}

func TestBuildAuthorizationContextClusterScopedResource(t *testing.T) {
	source := map[string]interface{}{
		"kind":            "Account",
		"name":            "account-a",
		"api_group":       "core.platform-mesh.io",
		"cluster_name":    "orgcluster1",
		"organization_id": "orgcluster1",
	}

	ctx, ok := buildAuthorizationContext(source)
	if !ok {
		t.Fatalf("expected valid authorization context")
	}
	expectedObject := "core_platform-mesh_io_account:orgcluster1/account-a"
	if ctx.object != expectedObject {
		t.Fatalf("expected object %q, got %q", expectedObject, ctx.object)
	}
}
