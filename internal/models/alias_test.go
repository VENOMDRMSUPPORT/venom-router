package models

import (
	"errors"
	"testing"
)

func TestAliasMap_ExactLookupHits(t *testing.T) {
	m := NewAliasMap()
	if err := m.Set("acme", "gpt-1", "model-1"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got, ok := m.Lookup("acme", "gpt-1")
	if !ok {
		t.Fatal("Lookup(acme, gpt-1) miss, want hit")
	}
	if got != "model-1" {
		t.Fatalf("Lookup(acme, gpt-1) = %s, want model-1", got)
	}
}

// TestAliasMap_NearMissIsAMiss proves the alias map is exact-match only:
// no case folding, no trimming, no prefix/family fallback.
func TestAliasMap_NearMissIsAMiss(t *testing.T) {
	m := NewAliasMap()
	if err := m.Set("acme", "gpt-1", "model-1"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	misses := []struct {
		provider string
		model    string
	}{
		{"Acme", "gpt-1"},   // case-different provider
		{"acme", "GPT-1"},   // case-different model
		{"acme", " gpt-1"},  // whitespace-different model
		{"acme", "gpt-1 "},  // trailing whitespace
		{"acm", "gpt-1"},    // prefix, not exact
		{"acme", "gpt-1x"},  // near-miss suffix
		{"unknown", "none"}, // wholly unrelated
	}

	for _, miss := range misses {
		if _, ok := m.Lookup(miss.provider, miss.model); ok {
			t.Fatalf("Lookup(%q, %q) hit, want miss (exact-match only)", miss.provider, miss.model)
		}
	}
}

func TestAliasMap_UnknownLookupMisses(t *testing.T) {
	m := NewAliasMap()
	if _, ok := m.Lookup("acme", "gpt-1"); ok {
		t.Fatal("Lookup on empty map hit, want miss")
	}
}

func TestAliasMap_Set_RejectsEmptyFields(t *testing.T) {
	m := NewAliasMap()
	if err := m.Set("", "gpt-1", "model-1"); !errors.Is(err, ErrEmptyAliasField) {
		t.Fatalf("Set with empty provider_id error = %v, want ErrEmptyAliasField", err)
	}
	if err := m.Set("acme", "", "model-1"); !errors.Is(err, ErrEmptyAliasField) {
		t.Fatalf("Set with empty provider_model_id error = %v, want ErrEmptyAliasField", err)
	}
	if err := m.Set("acme", "gpt-1", ""); !errors.Is(err, ErrEmptyAliasField) {
		t.Fatalf("Set with empty model_id error = %v, want ErrEmptyAliasField", err)
	}
}

func TestAliasMap_DistinctProvidersSameModelID(t *testing.T) {
	m := NewAliasMap()
	if err := m.Set("acme", "shared-id", "model-1"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := m.Set("other", "shared-id", "model-2"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got1, ok1 := m.Lookup("acme", "shared-id")
	got2, ok2 := m.Lookup("other", "shared-id")
	if !ok1 || !ok2 {
		t.Fatal("both provider-scoped entries should hit")
	}
	if got1 != "model-1" || got2 != "model-2" {
		t.Fatalf("got %s/%s, want model-1/model-2", got1, got2)
	}
}
