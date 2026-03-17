package fga

import (
	"reflect"
	"testing"

	"github.com/platform-mesh/search/internal/service/search"
)

func TestBuildBatchCheckItemResourceObjectFormat(t *testing.T) {
	hit := search.OpenSearchHit{Source: map[string]interface{}{
		"fga_object": "core_platform-mesh_io_component:cluster1/ns1/comp1",
		"permissions": []interface{}{
			map[string]interface{}{
				"user":     "core_platform-mesh_io_account:sap/workspaces",
				"relation": "parent",
				"object":   "core_platform_mesh_io_namespace:cluster1/ns1",
			},
		},
	}}

	item, missing := buildBatchCheckItem("alice@example.com", "get", 0, hit)
	if missing {
		t.Fatalf("expected context to be valid")
	}
	if item.TupleKey.Relation != "get" {
		t.Fatalf("unexpected relation: %s", item.TupleKey.Relation)
	}
	expected := "core_platform-mesh_io_component:cluster1/ns1/comp1"
	if item.TupleKey.Object != expected {
		t.Fatalf("unexpected object: %s", item.TupleKey.Object)
	}
	if len(item.ContextualTuples.TupleKeys) == 0 {
		t.Fatalf("expected contextual tuples")
	}
}

func TestBuildBatchCheckItemDropsMissingAuthContext(t *testing.T) {
	hit := search.OpenSearchHit{Source: map[string]interface{}{
		// missing fga_object
		"kind": "Component",
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

func TestFormatUser(t *testing.T) {
	tests := []struct {
		user string
		want string
	}{
		{"alice", "alice"},
		{"system:serviceaccount:default:auth", "system.serviceaccount.default.auth"},
	}
	for _, tt := range tests {
		if got := formatUser(tt.user); got != tt.want {
			t.Errorf("formatUser(%q) = %q, want %q", tt.user, got, tt.want)
		}
	}
}

func TestBuildAuthorizationContextFromDocumentMetadata(t *testing.T) {
	source := map[string]interface{}{
		"fga_object": "core_platform-mesh_io_component:cluster-x/ns-y/comp-z",
		"permissions": []interface{}{
			map[string]interface{}{
				"user":     "core_platform_mesh_io_account:sap/workspaces",
				"relation": "parent",
				"object":   "core_platform_mesh_io_namespace:cluster-x/ns-y",
			},
		},
	}

	ctx, ok := buildAuthorizationContext(source)
	if !ok {
		t.Fatalf("expected valid context")
	}

	if ctx.object != source["fga_object"] {
		t.Errorf("expected object %q, got %q", source["fga_object"], ctx.object)
	}
}

func TestBuildAuthorizationContextFromDocumentMetadataNoPermissions(t *testing.T) {
	source := map[string]interface{}{
		"fga_object": "core_platform_mesh_io_workspace:cluster-x/work-y",
	}

	ctx, ok := buildAuthorizationContext(source)
	if !ok {
		t.Fatalf("expected valid context")
	}

	if ctx.object != "core_platform_mesh_io_workspace:cluster-x/work-y" {
		t.Errorf("unexpected object: %s", ctx.object)
	}
	if len(ctx.contextualTuples) != 0 {
		t.Errorf("expected 0 tuples, got %d", len(ctx.contextualTuples))
	}
}
