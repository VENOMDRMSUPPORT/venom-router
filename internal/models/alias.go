package models

import "errors"

// ErrEmptyAliasField is returned when Set is given an empty provider_id,
// provider_model_id, or model_id.
var ErrEmptyAliasField = errors.New("models: alias fields must all be non-empty")

// aliasKey is the exact (provider_id, provider_model_id) pair AliasMap
// keys on. Go map equality on this struct is already exact-match — no
// normalization is ever applied to either field.
type aliasKey struct {
	providerID      string
	providerModelID string
}

// AliasMap is the sole exact identity map (02 §3):
// (provider_id, provider_model_id) -> model_id. Lookup is exact-match
// only; a case-, whitespace-, or prefix-different pair is a miss, never a
// fallback hit.
type AliasMap struct {
	entries map[aliasKey]string
}

// NewAliasMap returns an empty, ready-to-use AliasMap.
func NewAliasMap() *AliasMap {
	return &AliasMap{entries: make(map[aliasKey]string)}
}

// Set records that (providerID, providerModelID) maps to modelID. It fails
// closed on any empty field.
func (m *AliasMap) Set(providerID, providerModelID, modelID string) error {
	if providerID == "" || providerModelID == "" || modelID == "" {
		return ErrEmptyAliasField
	}
	m.entries[aliasKey{providerID: providerID, providerModelID: providerModelID}] = modelID
	return nil
}

// Lookup returns the model_id for the exact (providerID, providerModelID)
// pair, or ok=false on any miss — including a near-miss (case, whitespace,
// prefix) that is not byte-for-byte identical to a recorded key.
func (m *AliasMap) Lookup(providerID, providerModelID string) (modelID string, ok bool) {
	modelID, ok = m.entries[aliasKey{providerID: providerID, providerModelID: providerModelID}]
	return modelID, ok
}
