package httpapi

import (
	"net/http"
	"sort"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/platform"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/providers"
)

// ProvidersHandler serves the read-only provider catalog (03 §3/§4,
// P2b-PROV-002): the 11 built-in descriptors plus the custom
// OpenAI-compatible path, each annotated with capabilities derived
// from whatever adapters happen to be registered in reg (empty this
// phase — PROV-005/PROV-007 register the first live adapters) and a
// configured/missing_env flag for confidential-client OAuth providers.
// internal/providers stays pure (no os/env reads there); the env
// lookup lives here.
type ProvidersHandler struct {
	entries []providers.CatalogEntry
	reg     *providers.Registry
}

// NewProvidersHandler builds the handler over the built-in catalog plus
// the custom path descriptor, and reg (the shared, composition-root
// provider registry — empty until later units register live adapters).
func NewProvidersHandler(reg *providers.Registry) *ProvidersHandler {
	entries := append([]providers.CatalogEntry{}, providers.BuiltinCatalog()...)
	entries = append(entries, providers.CustomPathDescriptor())
	return &ProvidersHandler{entries: entries, reg: reg}
}

// providerJSON is one entry's wire shape for GET /providers[/{id}].
type providerJSON struct {
	ID           string   `json:"id"`
	DisplayName  string   `json:"display_name"`
	Description  string   `json:"description"`
	AuthMode     string   `json:"auth_mode"`
	BaseURL      string   `json:"base_url,omitempty"`
	Funding      any      `json:"funding"`
	Capabilities []string `json:"capabilities"`
	Configured   bool     `json:"configured"`
	MissingEnv   []string `json:"missing_env,omitempty"`
}

func (h *ProvidersHandler) toJSON(e providers.CatalogEntry) providerJSON {
	var missing []string
	for _, name := range e.RequiredEnv {
		if !platform.EnvPresent(name) {
			missing = append(missing, name)
		}
	}

	caps := providers.DerivedCapabilities(h.reg, e.ID)
	if caps == nil {
		caps = []string{}
	}

	fundingJSON := map[string]any{
		"mode":         string(e.Funding.Mode),
		"locked":       e.Funding.Locked,
		"non_expiring": e.Funding.NonExpiring,
	}
	if e.Funding.Fixed != "" {
		fundingJSON["fixed"] = string(e.Funding.Fixed)
	} else {
		fundingJSON["fixed"] = nil
	}

	return providerJSON{
		ID:           string(e.ID),
		DisplayName:  e.DisplayName,
		Description:  e.Description,
		AuthMode:     string(e.AuthMode),
		BaseURL:      e.BaseURL,
		Funding:      fundingJSON,
		Capabilities: caps,
		Configured:   len(missing) == 0,
		MissingEnv:   missing,
	}
}

// ServeList implements GET /providers: the full catalog, sorted by id
// for a stable, deterministic listing.
func (h *ProvidersHandler) ServeList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAuthError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", false)
		return
	}

	out := make([]providerJSON, 0, len(h.entries))
	for _, e := range h.entries {
		out = append(out, h.toJSON(e))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })

	writeAuthJSON(w, http.StatusOK, map[string]any{
		"data": map[string]any{"providers": out},
	})
}

// ServeGet implements GET /providers/{id}: a single entry, or
// not_found (404) for an unknown id.
func (h *ProvidersHandler) ServeGet(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAuthError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", false)
		return
	}

	id := r.PathValue("id")
	for _, e := range h.entries {
		if string(e.ID) == id {
			writeAuthJSON(w, http.StatusOK, map[string]any{"data": h.toJSON(e)})
			return
		}
	}
	writeAuthError(w, http.StatusNotFound, "not_found", "provider not found", false)
}
