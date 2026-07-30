package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/accounts/application"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/execution"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/observability"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/providers"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/quota"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/routing"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/secrets"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/storage"
)

// buildChatCompletionsHandler composes the real request-path engine + the chat
// handler for the shared control listener. It builds the request-path
// dispatcher (openai_compatible + native_oauth transports), the failure
// classifier, the candidate-snapshot builder over the real repos, and the usage
// writer. baseURLFor resolves each live provider's base URL (only opencode-zen
// is live in V1; others resolve to "" and fail closed at dispatch).
func buildChatCompletionsHandler(db *storage.DB, kr *secrets.Keyring, reg *providers.Registry) *ChatCompletionsHandler {
	httpClient := &http.Client{Timeout: execution.DefaultOpenAICompatibleTimeout}
	impls := map[execution.TransportType]execution.InferenceTransport{
		execution.TransportTypeOpenAICompatible: execution.NewOpenAICompatibleTransport(httpClient, 0),
		execution.TransportTypeNativeOAuth:      execution.NewNativeOAuthTransport(httpClient, 0),
	}
	baseURLs := map[string]string{
		string(providers.OpenCodeZenID): providers.OpenCodeZenBaseURL + "/v1",
	}
	credentialRepo := storage.NewAccountCredentialRepo(db)
	engine := &EngineDeps{
		Snapshot: NewSnapshotBuilder(
			storage.NewCatalogRepo(db), storage.NewAccountRepo(db),
			storage.NewFundingEvidenceRepo(db), credentialRepo,
			storage.NewQuotaWindowRepo(db, nil, nil), newInflightCounter(), 0,
		),
		Reservations:  storage.NewQuotaReservationRepo(db, nil),
		Lifecycle:     storage.NewQuotaLifecycleRepo(db, nil, nil),
		RouteRecorder: observability.NewRouteRecorder(db.Conn(), nil),
		Dispatcher:    BuildInferenceDispatcher(reg, impls),
		Classify:      NewDispatcherFailureClassifier(reg, impls),
		Creds:         credentialRepo,
		CredService:   application.NewCredentialService(credentialRepo, kr, nil),
		BaseURLFor:    func(providerID string) string { return baseURLs[providerID] },
		Inflight:      newInflightCounter(),
		Cache:         routing.NewStickinessCache(0),
		Now:           time.Now,
	}
	return NewChatCompletionsHandler(engine, storage.NewUsageRecordRepo(db), nil, nil)
}

// ChatCompletionsHandler serves POST /v1/chat/completions (P5-PAPI-002): the
// vk-gated OpenAI-compatible data-plane endpoint that runs the P4 engine. It
// records usage on EVERY terminal path (never swallowed) and never lets a
// prompt, response, raw provider error, or credential reach a record, log, or
// error body (05 §7).
type ChatCompletionsHandler struct {
	engine *EngineDeps
	usage  usageRecorder
	now    func() time.Time
	newID  func() string
}

// NewChatCompletionsHandler builds the handler. now/newID default to time.Now /
// newOAuthTransactionID (the generic id minter) when nil.
func NewChatCompletionsHandler(engine *EngineDeps, usage usageRecorder, now func() time.Time, newID func() string) *ChatCompletionsHandler {
	if now == nil {
		now = time.Now
	}
	if newID == nil {
		newID = newOAuthTransactionID
	}
	return &ChatCompletionsHandler{engine: engine, usage: usage, now: now, newID: newID}
}

// --- request wire types ----------------------------------------------------

type chatReq struct {
	Model      string           `json:"model"`
	Messages   []chatReqMsg     `json:"messages"`
	Stream     bool             `json:"stream"`
	MaxTokens  *int             `json:"max_tokens"`
	Tools      []chatReqToolIn  `json:"tools"`
	ToolChoice *json.RawMessage `json:"tool_choice"`
}

type chatReqMsg struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"` // string OR array
}

type chatReqToolIn struct {
	Type     string `json:"type"`
	Function struct {
		Name        string          `json:"name"`
		Description string          `json:"description"`
		Parameters  json.RawMessage `json:"parameters"`
	} `json:"function"`
}

type chatReqContentIn struct {
	Type     string `json:"type"`
	Text     string `json:"text"`
	ImageURL *struct {
		URL string `json:"url"`
	} `json:"image_url"`
}

// ServeHTTP lets the handler satisfy http.Handler for mux registration; it
// delegates to ServeChat.
func (h *ChatCompletionsHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.ServeChat(w, r)
}

// ServeChat is the handler entrypoint.
func (h *ChatCompletionsHandler) ServeChat(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writePublicError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
		return
	}

	var req chatReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writePublicError(w, http.StatusBadRequest, "invalid_request", "malformed JSON request body")
		return
	}

	tier, ok := tierForModel(req.Model)
	if !ok {
		writePublicError(w, http.StatusBadRequest, "invalid_request", "unknown model; \"model\" must be one of venom/lite, venom/pro, venom/max")
		return
	}
	if req.MaxTokens != nil && *req.MaxTokens < 0 {
		writePublicError(w, http.StatusBadRequest, "invalid_request", "\"max_tokens\" must not be negative")
		return
	}

	content, reqShape, err := h.buildContent(req)
	if err != nil {
		writePublicError(w, http.StatusBadRequest, "invalid_request", "invalid message content")
		return
	}

	reqs, err := routing.Normalize(reqShape)
	if err != nil {
		if !h.writePublicRoutingError(w, err) {
			writePublicError(w, http.StatusBadRequest, "invalid_request", "invalid request")
		}
		return
	}

	policies, err := routing.Policies()
	if err != nil {
		writePublicError(w, http.StatusInternalServerError, "internal_error", "an internal error occurred")
		return
	}

	requestID := h.newID()
	apiKeyID := apiKeyIDPtr(r.Context())

	h.run(w, r, runParams{
		req:       req,
		tier:      tier,
		policy:    policies[tier],
		reqs:      reqs,
		content:   content,
		requestID: requestID,
		apiKeyID:  apiKeyID,
	})
}

type runParams struct {
	req       chatReq
	tier      routing.Tier
	policy    routing.TierPolicy
	reqs      routing.Requirements
	content   execution.NormalizedRequest
	requestID string
	apiKeyID  *string
}

// run assembles the snapshot + engine input, runs the loop, then records the
// decision and usage on the terminal path and writes the response.
func (h *ChatCompletionsHandler) run(w http.ResponseWriter, r *http.Request, p runParams) {
	ctx := r.Context()

	snap, serr := h.engine.Snapshot.Build(ctx, p.tier, p.reqs)
	if serr != nil {
		// A request-level routing rejection (e.g. context exceeds tier) maps to
		// the public shape; anything else is an internal error. Usage is still
		// recorded for the terminal path.
		h.recordUsageBestEffort(ctx, p, routing.FallbackResult{}, serr)
		if !h.writePublicRoutingError(w, serr) {
			writePublicError(w, http.StatusInternalServerError, "internal_error", "an internal error occurred")
		}
		return
	}

	decisionID := h.newID()
	// The decision row MUST be persisted BEFORE the loop: every route_attempts
	// row the loop records FKs to route_decisions(id) (00011), foreign_keys is
	// ON, and RecordAttempt swallows a write error — so a decision recorded
	// after the loop would silently drop every attempt row. Chosen provider is
	// unknown pre-loop; it is captured on the usage_records row (billing truth)
	// and stamping it onto the decision is OBS-001's concern.
	h.recordDecisionBestEffort(ctx, decisionID, p, snap)

	var sink *sseSink
	plan := RequestPlan{
		Tier: p.tier, Policy: p.policy, Requirements: p.reqs,
		RequestID: p.requestID, DecisionID: decisionID,
		EstimateInput: quota.EstimateInput{MaxOutputTokens: p.req.MaxTokens}, // InputTokens nil = not counted (never 0)
		Content:       p.content, Stream: p.req.Stream,
	}
	if p.req.Stream {
		sink = newSSESink(w, p.req.Model, p.requestID)
		plan.Sink = sink
	}

	in := h.engine.BuildFallbackInput(plan, snap)
	res, lerr := routing.RunFallbackLoop(ctx, in)

	// Usage on EVERY terminal path — surfaced, never swallowed. On the
	// non-streaming path a write failure becomes a 500 before any body is sent;
	// on the streaming path (headers already flushed) it is logged loudly.
	usageErr := h.usage.Insert(ctx, buildUsageRecord(h.newID(), p.requestID, p.apiKeyID, string(p.tier), res, lerr))

	if p.req.Stream {
		h.finishStream(w, sink, lerr)
		return
	}
	if usageErr != nil {
		writePublicError(w, http.StatusInternalServerError, "internal_error", "an internal error occurred")
		return
	}
	if lerr != nil {
		if !h.writePublicRoutingError(w, lerr) {
			writePublicError(w, http.StatusServiceUnavailable, "internal_error", "an internal error occurred")
		}
		return
	}
	h.writeCompletion(w, p, res)
}

// finishStream terminates a streaming response: a clean [DONE] when the stream
// completed, or — if nothing was ever flushed — a mapped public error.
func (h *ChatCompletionsHandler) finishStream(w http.ResponseWriter, sink *sseSink, lerr error) {
	if !sink.headersWritten {
		if lerr != nil {
			if !h.writePublicRoutingError(w, lerr) {
				writePublicError(w, http.StatusServiceUnavailable, "internal_error", "an internal error occurred")
			}
			return
		}
		sink.ensureHeaders()
	}
	sink.writeDone()
}

// writeCompletion writes the non-streaming OpenAI chat.completion body.
func (h *ChatCompletionsHandler) writeCompletion(w http.ResponseWriter, p runParams, res routing.FallbackResult) {
	resp, _ := res.Response.(*execution.NormalizedResponse)
	if resp == nil {
		writePublicError(w, http.StatusInternalServerError, "internal_error", "an internal error occurred")
		return
	}
	toolCalls := make([]map[string]any, 0, len(resp.ToolCalls))
	for _, tc := range resp.ToolCalls {
		toolCalls = append(toolCalls, map[string]any{
			"type":     "function",
			"function": map[string]any{"name": tc.Name, "arguments": tc.ArgumentsJSON},
		})
	}
	message := map[string]any{"role": "assistant", "content": resp.Message.Content}
	if len(toolCalls) > 0 {
		message["tool_calls"] = toolCalls
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"id":      p.requestID,
		"object":  "chat.completion",
		"created": 0,
		"model":   p.req.Model,
		"choices": []map[string]any{{
			"index":         0,
			"message":       message,
			"finish_reason": resp.FinishReason,
		}},
	})
}

// recordDecisionBestEffort records one route_decision (RouteRecorder swallows
// its own write error — that is the route-record contract, distinct from usage).
func (h *ChatCompletionsHandler) recordDecisionBestEffort(ctx context.Context, decisionID string, p runParams, snap SnapshotResult) {
	_ = h.engine.RouteRecorder.RecordDecision(ctx, observability.RouteDecision{
		ID:                    decisionID,
		RequestID:             p.requestID,
		Tier:                  string(p.tier),
		WorkloadProfileBucket: "",
		CandidateSummary:      snap.Summary,
		ExclusionReasons:      snap.ExclusionReasons,
		CreatedAt:             h.now(),
	})
}

// recordUsageBestEffort records usage on an early terminal path (snapshot
// error) without a 500-on-failure gate — there is no successful response to
// protect here, so a usage write error is logged by the repo's caller.
func (h *ChatCompletionsHandler) recordUsageBestEffort(ctx context.Context, p runParams, res routing.FallbackResult, err error) {
	_ = h.usage.Insert(ctx, buildUsageRecord(h.newID(), p.requestID, p.apiKeyID, string(p.tier), res, err))
}

// buildContent maps the OpenAI request to the executor's NormalizedRequest
// content and the routing.Request shape used for Normalize.
func (h *ChatCompletionsHandler) buildContent(req chatReq) (execution.NormalizedRequest, routing.Request, error) {
	var content execution.NormalizedRequest
	var shape routing.Request
	shape.Stream = req.Stream

	var contextRunes int
	for _, m := range req.Messages {
		msg := execution.Message{Role: m.Role}
		parts, plain, hasImage, err := parseMessageContent(m.Content)
		if err != nil {
			return content, shape, err
		}
		if len(parts) > 0 {
			msg.Parts = parts
		} else {
			msg.Content = plain
		}
		content.Messages = append(content.Messages, msg)
		contextRunes += len([]rune(plain))
		for _, p := range parts {
			contextRunes += len([]rune(p.Text))
			if p.Kind == execution.ContentPartImage {
				hasImage = true
			}
		}
		shape.Parts = append(shape.Parts, routing.MessagePart{Kind: routing.PartText})
		if hasImage {
			shape.Parts = append(shape.Parts, routing.MessagePart{Kind: routing.PartImage})
		}
	}

	for _, t := range req.Tools {
		content.Tools = append(content.Tools, execution.ToolDefinition{
			Name: t.Function.Name, Description: t.Function.Description, ParametersJSON: string(t.Function.Parameters),
		})
	}
	if len(req.Tools) > 0 {
		shape.ToolsPresent = true
	}
	if req.ToolChoice != nil {
		var s string
		if json.Unmarshal(*req.ToolChoice, &s) == nil {
			content.ToolChoice = s
		}
	}
	content.MaxTokens = req.MaxTokens

	// Context need S: a coarse rune-count proxy (no tokenizer on this path yet),
	// floored at 1 so Normalize accepts it. A real token count is a later unit.
	if contextRunes < 1 {
		contextRunes = 1
	}
	shape.EstimatedContextTokens = int64(contextRunes)
	return content, shape, nil
}

// parseMessageContent decodes an OpenAI message content that is EITHER a plain
// string or the array-of-parts form.
func parseMessageContent(raw json.RawMessage) (parts []execution.ContentPart, plain string, hasImage bool, err error) {
	if len(raw) == 0 {
		return nil, "", false, nil
	}
	if json.Unmarshal(raw, &plain) == nil {
		return nil, plain, false, nil
	}
	var arr []chatReqContentIn
	if uerr := json.Unmarshal(raw, &arr); uerr != nil {
		return nil, "", false, uerr
	}
	for _, part := range arr {
		switch part.Type {
		case "text":
			parts = append(parts, execution.ContentPart{Kind: execution.ContentPartText, Text: part.Text})
		case "image_url":
			if part.ImageURL == nil {
				continue
			}
			cp := execution.ContentPart{Kind: execution.ContentPartImage}
			if mt, b64, ok := parseDataURL(part.ImageURL.URL); ok {
				cp.MediaType, cp.ImageBase64 = mt, b64
			} else {
				cp.ImageURL = part.ImageURL.URL
			}
			parts = append(parts, cp)
			hasImage = true
		}
	}
	return parts, "", hasImage, nil
}

// parseDataURL splits a data:<mediaType>;base64,<data> URL.
func parseDataURL(url string) (mediaType, base64Data string, ok bool) {
	if !strings.HasPrefix(url, "data:") {
		return "", "", false
	}
	rest := strings.TrimPrefix(url, "data:")
	semi := strings.Index(rest, ";base64,")
	if semi < 0 {
		return "", "", false
	}
	return rest[:semi], rest[semi+len(";base64,"):], true
}

// writePublicRoutingError renders a recognized routing error in the PUBLIC
// shape (reusing ROUTE-015's RoutingErrorFor mapping, not the control
// envelope), setting Retry-After when present. Returns false if err is not a
// routing error.
func (h *ChatCompletionsHandler) writePublicRoutingError(w http.ResponseWriter, err error) bool {
	env, ok := RoutingErrorFor(err)
	if !ok {
		return false
	}
	if env.RetryAfter != nil {
		w.Header().Set("Retry-After", strconv.Itoa(*env.RetryAfter))
	}
	writePublicError(w, env.HTTPStatus, env.Code, env.Message)
	return true
}

// --- SSE sink --------------------------------------------------------------

// sseSink writes streaming deltas as OpenAI-compatible SSE frames, flushing
// each so the client receives them progressively. Headers are written lazily on
// the first Emit so a pre-first-byte routing failure can still map to a proper
// error status.
type sseSink struct {
	w              http.ResponseWriter
	flusher        http.Flusher
	model          string
	id             string
	headersWritten bool
}

func newSSESink(w http.ResponseWriter, model, id string) *sseSink {
	f, _ := w.(http.Flusher)
	return &sseSink{w: w, flusher: f, model: model, id: id}
}

func (s *sseSink) ensureHeaders() {
	if s.headersWritten {
		return
	}
	s.w.Header().Set("Content-Type", "text/event-stream")
	s.w.Header().Set("Cache-Control", "no-cache")
	s.w.Header().Set("Connection", "keep-alive")
	s.w.WriteHeader(http.StatusOK)
	s.headersWritten = true
	if s.flusher != nil {
		s.flusher.Flush()
	}
}

func (s *sseSink) Emit(delta string) error {
	s.ensureHeaders()
	b, _ := json.Marshal(map[string]any{
		"id": s.id, "object": "chat.completion.chunk", "created": 0, "model": s.model,
		"choices": []map[string]any{{"index": 0, "delta": map[string]any{"content": delta}, "finish_reason": nil}},
	})
	if _, err := s.w.Write([]byte("data: " + string(b) + "\n\n")); err != nil {
		return err
	}
	if s.flusher != nil {
		s.flusher.Flush()
	}
	return nil
}

func (s *sseSink) writeDone() {
	s.ensureHeaders()
	_, _ = s.w.Write([]byte("data: [DONE]\n\n"))
	if s.flusher != nil {
		s.flusher.Flush()
	}
}

// tierForModel resolves a public "venom/<tier>" model name to its tier via the
// routing vocabulary (publicTierOrder), never three bare literals.
func tierForModel(model string) (routing.Tier, bool) {
	for _, tier := range publicTierOrder {
		if model == publicModelName(tier) {
			return tier, true
		}
	}
	return "", false
}

// apiKeyIDPtr returns the authenticated key id from ctx as a pointer (nil when
// absent — should not happen behind vk auth, but nil is the honest value).
func apiKeyIDPtr(ctx context.Context) *string {
	if id, ok := apiKeyIDFromContext(ctx); ok && id != "" {
		return &id
	}
	return nil
}
