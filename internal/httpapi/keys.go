package httpapi

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/storage"
)

// keysCreateRoute is the fixed idempotency route key POST /keys replays under
// (matching route+Idempotency-Key pairs, not the request path).
const keysCreateRoute = "POST /keys"

// vkRawEntropyBytes is the number of CSPRNG bytes in a raw vk_live_* key: 32
// bytes = 256 bits, hex-encoded to 64 chars. 09 §3.11 / 01 §8: a Venom key is
// a high-entropy random secret, shown once, stored only as its hash.
const vkRawEntropyBytes = 32

// keyPrefixFragmentLen is how many leading hex chars of the random part are
// kept in the NON-SECRET key_prefix for display (key_prefix = "vk_live_" +
// this many hex chars). 4 hex chars = 16 bits: enough to tell two keys apart
// in the UI, negligible against a brute force (the remaining 240 bits stay
// secret), and — crucially — the raw key itself is never stored, so the prefix
// is the only on-disk fragment and it is deliberately far too short to help an
// attacker.
const keyPrefixFragmentLen = 4

// KeysHandler serves the P5-CAPI-001 Venom API-key management surface:
// POST /keys, GET /keys, DELETE /keys/{id}. Owner-session + CSRF gated (via
// ControlMux's `gated`) and, for the create, Idempotency-Key aware.
type KeysHandler struct {
	repo  *storage.APIKeyRepo
	idem  *idempotencyStore
	audit *auditEmitter
	now   func() time.Time
	genID func() string
}

// NewKeysHandler builds the handler. now defaults to time.Now, genID to
// newOAuthTransactionID (the generic high-entropy id minter) when nil.
func NewKeysHandler(repo *storage.APIKeyRepo, idem *idempotencyStore, audit *auditEmitter, now func() time.Time, genID func() string) *KeysHandler {
	if now == nil {
		now = time.Now
	}
	if genID == nil {
		genID = newOAuthTransactionID
	}
	return &KeysHandler{repo: repo, idem: idem, audit: audit, now: now, genID: genID}
}

// createKeyRequest is POST /keys' body.
type createKeyRequest struct {
	Label    string `json:"label"`
	RPMLimit *int   `json:"rpm_limit,omitempty"`
}

// createKeyResponse is POST /keys' 201 payload. raw_key is present ONLY here,
// exactly once — no read path ever returns it again (09 §3.11).
type createKeyResponse struct {
	ID       string `json:"id"`
	Label    string `json:"label"`
	RPMLimit *int   `json:"rpm_limit"`
	RawKey   string `json:"raw_key"`
}

// keyJSON is the read projection for GET /keys: it carries the non-secret
// display prefix but NEVER the raw key (there is none stored) and NEVER the
// full verifier hash.
type keyJSON struct {
	ID         string `json:"id"`
	Label      string `json:"label"`
	RPMLimit   *int   `json:"rpm_limit"`
	KeyPrefix  string `json:"key_prefix"`
	CreatedAt  int64  `json:"created_at"`
	LastUsedAt *int64 `json:"last_used_at"`
	RevokedAt  *int64 `json:"revoked_at"`
}

// ServeCollection dispatches POST (create) and GET (list) on
// /api/control/v1/keys. The method check is outside idem.Execute so a
// wrong-method request is never captured as a replayable response.
func (h *KeysHandler) ServeCollection(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		h.idem.Execute(w, r, keysCreateRoute, h.serveCreate)
	case http.MethodGet:
		h.serveList(w, r)
	default:
		writeAuthError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", false)
	}
}

// serveCreate is POST /keys' body, run at most once per (route, Idempotency-Key).
func (h *KeysHandler) serveCreate(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var req createKeyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.audit.Emit(ctx, AuditActionKeyCreate, AuditResultFailure, AuditResourceAPIKey, "", "validation_error")
		writeAuthError(w, http.StatusBadRequest, "validation_error", "invalid request body", false)
		return
	}
	if strings.TrimSpace(req.Label) == "" {
		h.audit.Emit(ctx, AuditActionKeyCreate, AuditResultFailure, AuditResourceAPIKey, "", "validation_error")
		writeAuthError(w, http.StatusBadRequest, "validation_error", "label is required", false)
		return
	}
	if req.RPMLimit != nil && *req.RPMLimit <= 0 {
		h.audit.Emit(ctx, AuditActionKeyCreate, AuditResultFailure, AuditResourceAPIKey, "", "validation_error")
		writeAuthError(w, http.StatusBadRequest, "validation_error", "rpm_limit must be a positive integer", false)
		return
	}

	raw, err := newRawAPIKey()
	if err != nil {
		h.audit.Emit(ctx, AuditActionKeyCreate, AuditResultFailure, AuditResourceAPIKey, "", "internal_error")
		writeAuthError(w, http.StatusInternalServerError, "internal", "could not generate key", true)
		return
	}
	id := h.genID()
	if err := h.repo.Create(ctx, storage.CreateAPIKeyParams{
		ID:        id,
		Label:     req.Label,
		KeyHash:   HashAPIKey(raw),                                 // ONLY the hash is stored
		KeyPrefix: raw[:len(vkLiveKeyPrefix)+keyPrefixFragmentLen], // non-secret display fragment
		RPMLimit:  req.RPMLimit,
		CreatedAt: h.now(),
	}); err != nil {
		h.audit.Emit(ctx, AuditActionKeyCreate, AuditResultFailure, AuditResourceAPIKey, id, "internal_error")
		writeAuthError(w, http.StatusInternalServerError, "internal", "could not create key", true)
		return
	}

	// Exactly one secret-free audit row: ids/labels only — never the raw key or
	// the hash.
	h.audit.Emit(ctx, AuditActionKeyCreate, AuditResultSuccess, AuditResourceAPIKey, id, "")
	writeData(w, http.StatusCreated, createKeyResponse{ID: id, Label: req.Label, RPMLimit: req.RPMLimit, RawKey: raw})
}

// serveList is GET /keys: every key, projected without the raw key or the full
// hash.
func (h *KeysHandler) serveList(w http.ResponseWriter, r *http.Request) {
	keys, err := h.repo.List(r.Context())
	if err != nil {
		writeAuthError(w, http.StatusInternalServerError, "internal", "could not list keys", true)
		return
	}
	out := make([]keyJSON, 0, len(keys))
	for _, k := range keys {
		out = append(out, toKeyJSON(k))
	}
	writeData(w, http.StatusOK, out)
}

// ServeDelete is DELETE /keys/{id}: revoke the key, idempotently.
func (h *KeysHandler) ServeDelete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		writeAuthError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", false)
		return
	}
	ctx := r.Context()
	id := r.PathValue("id")
	if id == "" {
		writeAuthError(w, http.StatusBadRequest, "validation_error", "key id is required", false)
		return
	}
	if err := h.repo.Revoke(ctx, id, h.now()); err != nil {
		h.audit.Emit(ctx, AuditActionKeyRevoke, AuditResultFailure, AuditResourceAPIKey, id, "internal_error")
		writeAuthError(w, http.StatusInternalServerError, "internal", "could not revoke key", true)
		return
	}
	h.audit.Emit(ctx, AuditActionKeyRevoke, AuditResultSuccess, AuditResourceAPIKey, id, "")
	writeData(w, http.StatusOK, map[string]any{"id": id, "status": "revoked"})
}

func toKeyJSON(k storage.APIKey) keyJSON {
	out := keyJSON{
		ID:        k.ID,
		Label:     k.Label,
		RPMLimit:  k.RPMLimit,
		KeyPrefix: k.KeyPrefix,
		CreatedAt: k.CreatedAt.Unix(),
	}
	if k.LastUsedAt != nil {
		v := k.LastUsedAt.Unix()
		out.LastUsedAt = &v
	}
	if k.RevokedAt != nil {
		v := k.RevokedAt.Unix()
		out.RevokedAt = &v
	}
	return out
}

// newRawAPIKey generates a fresh raw key: "vk_live_" + hex(32 CSPRNG bytes).
// The raw value exists only in this return and the single 201 response body;
// storage keeps only its hash.
func newRawAPIKey() (string, error) {
	b := make([]byte, vkRawEntropyBytes)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return vkLiveKeyPrefix + hex.EncodeToString(b), nil
}
