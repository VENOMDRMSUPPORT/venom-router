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

// usageWriteTimeout bounds the detached usage accounting write. It deliberately
// outlives a cancelled client request (see run's comment on why the usage
// context must not be the request context), so it needs its own deadline rather
// than inheriting the request's.
const usageWriteTimeout = 5 * time.Second

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
		execution.TransportTypeNativeAPI:        execution.NewNativeAPITransport(httpClient, 0),
	}
	baseURLs := map[string]string{
		string(providers.OpenCodeZenID): providers.OpenCodeZenBaseURL + "/v1",
		string(providers.OllamaCloudID): providers.OllamaCloudBaseURL,
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
		// The owner's configured quota-staleness window (P6-CAPI-001, 05 §4),
		// read once per request inside BuildFallbackInput. Without this the
		// stored setting would be inert on the request path.
		StaleAfter: newOperationalSettings(storage.NewSettingsRepo(db)).stalenessWindow,
		Cache:      routing.NewStickinessCache(0),
		Now:        time.Now,
	}
	return NewChatCompletionsHandler(engine, storage.NewUsageRecordRepo(db), nil, nil, nil)
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
	log    *observability.Logger
}

// NewChatCompletionsHandler builds the handler. now/newID default to time.Now /
// newOAuthTransactionID (the generic id minter) when nil; log defaults to
// observability.Default().
func NewChatCompletionsHandler(engine *EngineDeps, usage usageRecorder, now func() time.Time, newID func() string, log *observability.Logger) *ChatCompletionsHandler {
	if now == nil {
		now = time.Now
	}
	if newID == nil {
		newID = newOAuthTransactionID
	}
	if log == nil {
		log = observability.Default()
	}
	return &ChatCompletionsHandler{engine: engine, usage: usage, now: now, newID: newID, log: log}
}

// --- request wire types ----------------------------------------------------

type chatReq struct {
	Model      string           `json:"model"`
	Messages   []chatReqMsg     `json:"messages"`
	Stream     bool             `json:"stream"`
	MaxTokens  *int             `json:"max_tokens"`
	Tools      []chatReqToolIn  `json:"tools"`
	ToolChoice *json.RawMessage `json:"tool_choice"`
	// Venom is the OPTIONAL venom request extension (05 §1b), parsed strictly
	// (unknown fields rejected) by parseVenomExtension. It is a RawMessage so
	// the OUTER decode stays lenient (unknown top-level OpenAI fields ignored,
	// SDK parity) while the sub-object alone is validated with
	// DisallowUnknownFields.
	Venom json.RawMessage `json:"venom"`
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
//
// Idempotency boundary (P5-PAPI-006): unlike the control plane's mutations,
// /v1/chat/completions deliberately does NOT honor an Idempotency-Key. Inference
// is not replay-idempotent — usage is recorded per terminal path, so replaying a
// completion would double-bill or resurrect a stale response. The header is
// ignored (never read), so two identical requests with the same key both
// execute and both record usage. Making the choice explicit here keeps it a
// decision rather than an accident.
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

	// Parse + validate the optional venom extension BEFORE any routing runs, so
	// an invalid extension is a 400 that executes NOTHING. Unknown fields inside
	// venom are rejected here (naming the field); the outer body decode above
	// stayed lenient for unknown top-level fields (SDK parity).
	ext, err := parseVenomExtension(req.Venom)
	if err != nil {
		writePublicError(w, http.StatusBadRequest, CodeInvalidExtension, venomExtErrorMessage(err))
		return
	}
	reqShape.Venom = ext

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
	// Stamp the request id as soon as it exists, BEFORE anything can fail. Every
	// later error response reuses it (writePublicErrorRetryable reads it back), so
	// the request_id a client sees on a failure is the SAME id written to
	// usage_records / route_decisions / route_attempts and is therefore findable.
	stampRequestID(w.Header(), requestID)
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
		sink = newSSESink(w, p.req.Model, string(p.tier), p.requestID)
		plan.Sink = sink
	}

	start := h.now()
	in := h.engine.BuildFallbackInput(ctx, plan, snap)
	res, lerr := routing.RunFallbackLoop(ctx, in)
	// Latency is a REAL measurement across the executed loop (never hardcoded);
	// floored at 0 so a non-monotonic injected clock can never go negative.
	latencyMS := h.now().Sub(start).Milliseconds()
	if latencyMS < 0 {
		latencyMS = 0
	}

	// Usage on EVERY terminal path — surfaced, never swallowed. On the
	// non-streaming path a write failure becomes a 500 before any body is sent.
	//
	// THE CONTEXT MUST BE DETACHED FROM THE CLIENT CONNECTION. Using ctx here
	// loses the row on exactly the path that most needs it: when the client
	// disconnects mid-request, ctx is already cancelled, so the INSERT fails with
	// context.Canceled and the request is served (partially) with no usage/billing
	// record at all — the card's "never swallowed (the old build's bug)" failure,
	// reintroduced by cancellation rather than by a missing call. Cancellation must
	// abort the PROVIDER call (it does, via ctx) but never the accounting write.
	// Same reasoning as P3a-CAPI-002's detached discovery context.
	usageCtx, cancelUsage := context.WithTimeout(context.WithoutCancel(ctx), usageWriteTimeout)
	defer cancelUsage()
	usageErr := h.usage.Insert(usageCtx, buildUsageRecord(h.newID(), p.requestID, p.apiKeyID, string(p.tier), res, lerr))

	tel := h.telemetryFor(p, res, latencyMS)

	if p.req.Stream {
		// A streaming response cannot become a 500 once frames have been flushed,
		// so the write failure is LOGGED here rather than returned. It must never
		// simply vanish: an unrecorded usage row is silent revenue/quota loss, and
		// "never swallowed" is this card's explicit requirement. (Before this, the
		// error was assigned and then discarded on this branch, with a comment
		// claiming it was logged — it was not.)
		if usageErr != nil {
			h.log.Error("usage record write failed on the streaming path",
				observability.Err(usageErr),
				observability.String("request_id", p.requestID),
				observability.String("tier", string(p.tier)))
		}
		h.finishStream(w, sink, lerr, tel)
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
	h.writeCompletion(w, p, res, tel)
}

// telemetryFor assembles the X-Venom-* fact set from the served result. Tokens
// are UNKNOWN on this path (no provider-reported usage is plumbed yet), so their
// pointers stay nil and their headers are omitted — never fabricated as 0.
// Funding is likewise not carried on FallbackResult (the authorized PAPI-004
// additive change is thinking-only), so it is omitted. AccountID is deliberately
// never read here.
func (h *ChatCompletionsHandler) telemetryFor(p runParams, res routing.FallbackResult, latencyMS int64) venomTelemetry {
	tel := venomTelemetry{
		RequestID: p.requestID,
		Tier:      string(p.tier),
		Provider:  res.ProviderID,
		Model:     p.req.Model,
		LatencyMS: &latencyMS,
		Thinking:  string(res.ThinkingApplied),
	}
	if res.Attempts > 0 {
		n := res.Attempts
		tel.Attempts = &n
	}
	// The clamp indicator is meaningful only for a served attempt (one that
	// produced a thinking decision, i.e. a provider was chosen). It is the OR of
	// the tier-ceiling and per-offering certified-max clamps.
	if res.ProviderID != "" {
		clamped := res.ThinkingTierClamped || res.ThinkingCertifiedClamped
		tel.ThinkingClamped = &clamped
	}
	return tel
}

// finishStream terminates a streaming response: the FINAL telemetry as a
// trailing SSE comment and then a clean [DONE] when the stream completed, or —
// if nothing was ever flushed — a mapped public error. The trailer is written
// BEFORE [DONE] so [DONE] stays the last frame a plain OpenAI SDK sees.
func (h *ChatCompletionsHandler) finishStream(w http.ResponseWriter, sink *sseSink, lerr error, tel venomTelemetry) {
	if !sink.headersWritten {
		if lerr != nil {
			if !h.writePublicRoutingError(w, lerr) {
				writePublicError(w, http.StatusServiceUnavailable, "internal_error", "an internal error occurred")
			}
			return
		}
		sink.ensureHeaders()
	}
	sink.writeTrailer(tel)
	sink.writeDone()
}

// writeCompletion writes the non-streaming OpenAI chat.completion body.
func (h *ChatCompletionsHandler) writeCompletion(w http.ResponseWriter, p runParams, res routing.FallbackResult, tel venomTelemetry) {
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
	// Stamp the FULL X-Venom-* set BEFORE WriteHeader so the client sees the
	// routing outcome on the completion response. The response BODY below never
	// carries the provider or account id (privacy split, 01 §6c / constraint 3);
	// the header set does carry provider + model.
	writeVenomHeaders(w.Header(), tel)
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
	// RequestedThinking is known pre-loop (from the normalized request) and is
	// recorded now. The APPLIED level + clamp flags are known only after the
	// served attempt and are reported on the X-Venom-* headers/trailer; stamping
	// them onto this row follows the same post-loop decision-update path as
	// ChosenProviderID (OBS-001), which the pre-loop FK ordering defers.
	var requested string
	if p.reqs.RequestedThinking != nil {
		requested = string(*p.reqs.RequestedThinking)
	}
	_ = h.engine.RouteRecorder.RecordDecision(ctx, observability.RouteDecision{
		ID:                    decisionID,
		RequestID:             p.requestID,
		Tier:                  string(p.tier),
		WorkloadProfileBucket: "",
		CandidateSummary:      snap.Summary,
		ExclusionReasons:      snap.ExclusionReasons,
		RequestedThinking:     requested,
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
	// Retryable comes from the routing envelope, never re-derived from the code.
	writePublicErrorRetryable(w, env.HTTPStatus, env.Code, env.Message, env.Retryable)
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
	tier           string
	id             string
	headersWritten bool
}

func newSSESink(w http.ResponseWriter, model, tier, id string) *sseSink {
	f, _ := w.(http.Flusher)
	return &sseSink{w: w, flusher: f, model: model, tier: tier, id: id}
}

func (s *sseSink) ensureHeaders() {
	if s.headersWritten {
		return
	}
	// Stream-start X-Venom-* set (01 §6c): the known identity facts plus ZEROED
	// metrics (the documented exception to the omit-unknown rule) — the true
	// metrics arrive in the trailing comment once the stream completes. Stamped
	// BEFORE WriteHeader so they reach the client with the first byte.
	writeVenomHeaders(s.w.Header(), streamStartTelemetry(s.id, s.tier, s.model))
	s.w.Header().Set("Content-Type", "text/event-stream")
	s.w.Header().Set("Cache-Control", "no-cache")
	s.w.Header().Set("Connection", "keep-alive")
	s.w.WriteHeader(http.StatusOK)
	s.headersWritten = true
	if s.flusher != nil {
		s.flusher.Flush()
	}
}

// writeTrailer emits the FINAL telemetry as a trailing SSE comment line. It is
// called immediately before writeDone, so the comment precedes data: [DONE] and
// a comment-ignoring SSE reader still sees [DONE] as the last event.
func (s *sseSink) writeTrailer(t venomTelemetry) {
	s.ensureHeaders()
	_, _ = s.w.Write([]byte(venomTrailerComment(t)))
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
