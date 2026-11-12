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

package search

import (
	"encoding/json"
	"errors"
	"testing"
)

func TestCursorRoundTrip(t *testing.T) {
	state := CursorState{
		Version:     cursorVersion,
		Org:         "acme",
		QueryHash:   queryHash("foo"),
		Mode:        SearchModeSemantic,
		Limit:       20,
		SearchAfter: []any{1.23, "abc"},
	}

	encoded, err := EncodeCursor(state)
	if err != nil {
		t.Fatalf("EncodeCursor returned error: %v", err)
	}

	decoded, err := DecodeCursor(encoded)
	if err != nil {
		t.Fatalf("DecodeCursor returned error: %v", err)
	}

	if decoded.Org != state.Org || decoded.QueryHash != state.QueryHash || decoded.Mode != state.Mode || decoded.Limit != state.Limit {
		t.Fatalf("decoded cursor mismatch: %+v", decoded)
	}
	if len(decoded.SearchAfter) != 2 {
		t.Fatalf("unexpected search_after length: %d", len(decoded.SearchAfter))
	}
}

func TestCursorRoundTripPreservesFieldsHash(t *testing.T) {
	state := CursorState{
		Version:     cursorVersion,
		Org:         "acme",
		QueryHash:   queryHash("foo"),
		FieldsHash:  cursorFieldsHash([]string{"kind", "name"}),
		Limit:       20,
		SearchAfter: []any{1.0, "x"},
	}

	encoded, err := EncodeCursor(state)
	if err != nil {
		t.Fatalf("EncodeCursor returned error: %v", err)
	}
	decoded, err := DecodeCursor(encoded)
	if err != nil {
		t.Fatalf("DecodeCursor returned error: %v", err)
	}
	if decoded.FieldsHash != state.FieldsHash {
		t.Fatalf("FieldsHash = %q, want %q", decoded.FieldsHash, state.FieldsHash)
	}
}

func TestValidateCursorMismatch(t *testing.T) {
	state := CursorState{Version: cursorVersion, Org: "acme", QueryHash: queryHash("foo"), Mode: SearchModeLexical, Limit: 20, SearchAfter: []any{1.0, "x"}}
	if err := ValidateCursor(state, "other", queryHash("foo"), SearchModeLexical, "", "", "", "", 20); err == nil {
		t.Fatalf("expected org mismatch error")
	}
	if err := ValidateCursor(state, "acme", queryHash("bar"), SearchModeLexical, "", "", "", "", 20); err == nil {
		t.Fatalf("expected query mismatch error")
	}
	if err := ValidateCursor(state, "acme", queryHash("foo"), SearchModeSemantic, "", "", "", "", 20); err == nil {
		t.Fatalf("expected mode mismatch error")
	}
	if err := ValidateCursor(state, "acme", queryHash("foo"), SearchModeLexical, "", "", "", "", 30); err == nil {
		t.Fatalf("expected limit mismatch error")
	}
	if err := ValidateCursor(state, "acme", queryHash("foo"), SearchModeLexical, "", "", "", "name:asc", 20); err == nil {
		t.Fatalf("expected sort mismatch error")
	}
}

func TestValidateCursorFieldsMismatch(t *testing.T) {
	state := CursorState{
		Version:     cursorVersion,
		Org:         "acme",
		QueryHash:   queryHash("foo"),
		FieldsHash:  cursorFieldsHash([]string{"kind"}),
		Limit:       10,
		SearchAfter: []any{1.0, "1"},
	}
	err := ValidateCursor(state, "acme", queryHash("foo"), "", "", "", cursorFieldsHash([]string{"name"}), "", 10)
	if err == nil {
		t.Fatal("expected error for fields mismatch")
	}
	if !errors.Is(err, ErrInvalidCursor) {
		t.Fatalf("expected ErrInvalidCursor, got %v", err)
	}
}

func TestValidateCursorFieldsMatchEmpty(t *testing.T) {
	// cursor without fields must be valid when request also has no fields
	state := CursorState{
		Version:     cursorVersion,
		Org:         "acme",
		QueryHash:   queryHash("foo"),
		Limit:       10,
		SearchAfter: []any{1.0, "1"},
	}
	if err := ValidateCursor(state, "acme", queryHash("foo"), "", "", "", "", "", 10); err != nil {
		t.Fatalf("expected no error for matching empty fields hash, got %v", err)
	}
}

func TestCursorFieldsHashIsOrderIndependent(t *testing.T) {
	h1 := cursorFieldsHash([]string{"kind", "name"})
	h2 := cursorFieldsHash([]string{"name", "kind"})
	if h1 != h2 {
		t.Fatalf("expected same hash for same field set in different order: %q vs %q", h1, h2)
	}
}

func TestCursorFieldsHashEmptyIsEmpty(t *testing.T) {
	if h := cursorFieldsHash(nil); h != "" {
		t.Fatalf("expected empty hash for nil fields, got %q", h)
	}
	if h := cursorFieldsHash([]string{}); h != "" {
		t.Fatalf("expected empty hash for empty fields, got %q", h)
	}
}

func TestCursorRoundTripKeepsLongSortValuesExact(t *testing.T) {
	state := CursorState{
		Version:     cursorVersion,
		Org:         "acme",
		QueryHash:   queryHash("foo"),
		Sort:        "spec.stars:asc",
		Limit:       20,
		SearchAfter: []any{json.Number("9223372036854775807"), json.Number("1.5"), "abc", nil},
	}

	encoded, err := EncodeCursor(state)
	if err != nil {
		t.Fatalf("EncodeCursor returned error: %v", err)
	}
	decoded, err := DecodeCursor(encoded)
	if err != nil {
		t.Fatalf("DecodeCursor returned error: %v", err)
	}

	raw, err := json.Marshal(decoded.SearchAfter)
	if err != nil {
		t.Fatalf("marshal search_after: %v", err)
	}
	if got, want := string(raw), `[9223372036854775807,1.5,"abc",null]`; got != want {
		t.Fatalf("search_after = %s, want %s", got, want)
	}
	if decoded.Sort != state.Sort {
		t.Fatalf("sort = %q, want %q", decoded.Sort, state.Sort)
	}
}
