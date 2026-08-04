package execution

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// The required headers on EVERY authenticated Anthropic call (03 §3 claude-code):
// without them the provider answers 429, so a missing-header regression is a
// real outage, not cosmetic. anthropicBetaHeader carries the verified
// oauth-2025-04-20 beta; a claude-code-specific beta string may need to be
// appended after live re-verification (see the batch report).
const (
	anthropicVersionHeader   = "2023-06-01"
	anthropicBetaHeader      = "oauth-2025-04-20"
	anthropicAppHeader       = "cli"
	anthropicUserAgentHeader = "claude-cli/0.1 (venom-router)"
)

// This file holds the Anthropic Messages wire mapping — one of the schemas the
// native_oauth transport serves (P7-EXEC-001 part 2, consumed by claude-code
// P7-PROV-001). It mirrors geminiwire.go's structure: request builder, success
// decoder, SSE runner. The transport picks this mapping when
// route.WireSchema == WireSchemaAnthropicMessages.

// DefaultAnthropicMaxTokens is the max_tokens sent when a request carries none.
// Anthropic REQUIRES max_tokens on every /v1/messages call (unlike OpenAI and
// Gemini, where it is optional), so a request without one uses this documented
// default rather than omitting the field (which the provider rejects).
const DefaultAnthropicMaxTokens = 4096

// Anthropic Messages request wire types ------------------------------------

// anthropicImageSource is the base64 image block Anthropic accepts. A URL-only
// image is NOT expressible here (Anthropic's inline source needs the bytes), so
// it fails closed.
type anthropicImageSource struct {
	Type      string `json:"type"`       // "base64"
	MediaType string `json:"media_type"` // e.g. image/png
	Data      string `json:"data"`
}

type anthropicContentBlock struct {
	Type   string                `json:"type"` // "text" | "image"
	Text   string                `json:"text,omitempty"`
	Source *anthropicImageSource `json:"source,omitempty"`
}

type anthropicMessage struct {
	Role    string `json:"role"` // "user" | "assistant"
	Content any    `json:"content"`
}

// anthropicTool maps a client tool definition: input_schema carries the
// client's JSON Schema forwarded verbatim (embedded raw JSON, never re-encoded).
type anthropicTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"input_schema,omitempty"`
}

type anthropicMessagesRequest struct {
	Model     string             `json:"model"`
	MaxTokens int                `json:"max_tokens"`
	System    string             `json:"system,omitempty"`
	Messages  []anthropicMessage `json:"messages"`
	Stream    bool               `json:"stream,omitempty"`
	Tools     []anthropicTool    `json:"tools,omitempty"`
}

// buildAnthropicRequest maps a normalized request onto Anthropic's Messages
// shape, failing CLOSED (ErrRequestFeatureUnsupported) on a URL-only image, an
// unknown content-part kind, or a tool_choice directive (no faithful mapping).
// System messages go to the TOP-LEVEL system field (Anthropic has no system
// role in messages — the same shape Gemini's systemInstruction needs, and the
// same defect the last batch fixed): sending them as a messages turn would make
// the model appear to have said the system prompt. Multiple system messages
// concatenate in order. max_tokens is ALWAYS present (DefaultAnthropicMaxTokens
// when the request carries none), because Anthropic rejects a request without it.
func buildAnthropicRequest(req NormalizedRequest, model string, stream bool) (anthropicMessagesRequest, error) {
	if req.ToolChoice != "" {
		return anthropicMessagesRequest{}, fmt.Errorf("%w: tool_choice", ErrRequestFeatureUnsupported)
	}
	out := anthropicMessagesRequest{Model: model, Stream: stream}
	var systemParts []string
	for _, m := range req.Messages {
		if m.Role == "system" {
			systemParts = append(systemParts, anthropicSystemText(m))
			continue
		}
		content, err := anthropicContentFor(m)
		if err != nil {
			return anthropicMessagesRequest{}, err
		}
		out.Messages = append(out.Messages, anthropicMessage{Role: anthropicRoleFor(m.Role), Content: content})
	}
	out.System = strings.Join(systemParts, "\n\n")

	out.MaxTokens = DefaultAnthropicMaxTokens
	if req.MaxTokens != nil && *req.MaxTokens > 0 {
		out.MaxTokens = *req.MaxTokens
	}

	if len(req.Tools) > 0 {
		tools := make([]anthropicTool, 0, len(req.Tools))
		for _, td := range req.Tools {
			at := anthropicTool{Name: td.Name, Description: td.Description}
			if td.ParametersJSON != "" {
				at.InputSchema = json.RawMessage(td.ParametersJSON)
			}
			tools = append(tools, at)
		}
		out.Tools = tools
	}
	return out, nil
}

// anthropicSystemText returns a system message's text (Parts concatenated when
// multimodal, else the plain content).
func anthropicSystemText(m Message) string {
	if len(m.Parts) == 0 {
		return m.Content
	}
	var b strings.Builder
	for _, p := range m.Parts {
		if p.Kind == ContentPartText {
			b.WriteString(p.Text)
		}
	}
	return b.String()
}

// anthropicRoleFor maps Venom's roles onto Anthropic's (user | assistant).
// System never reaches here (routed to the top-level field first).
func anthropicRoleFor(role string) string {
	if role == "assistant" {
		return "assistant"
	}
	return "user"
}

// anthropicContentFor returns a message's content as the plain string (no
// Parts) or the block array (Parts present), failing closed on an inexpressible
// part.
func anthropicContentFor(m Message) (any, error) {
	if len(m.Parts) == 0 {
		return m.Content, nil
	}
	blocks := make([]anthropicContentBlock, 0, len(m.Parts))
	for _, p := range m.Parts {
		switch p.Kind {
		case ContentPartText:
			blocks = append(blocks, anthropicContentBlock{Type: "text", Text: p.Text})
		case ContentPartImage:
			if p.ImageBase64 == "" || p.MediaType == "" {
				return nil, fmt.Errorf("%w: image part requires inline base64 data and a media type", ErrRequestFeatureUnsupported)
			}
			blocks = append(blocks, anthropicContentBlock{Type: "image", Source: &anthropicImageSource{Type: "base64", MediaType: p.MediaType, Data: p.ImageBase64}})
		default:
			return nil, fmt.Errorf("%w: content part kind %q", ErrRequestFeatureUnsupported, p.Kind)
		}
	}
	return blocks, nil
}

// Anthropic Messages response wire types -----------------------------------

type anthropicRespBlock struct {
	Type  string          `json:"type"` // "text" | "tool_use"
	Text  string          `json:"text"`
	Name  string          `json:"name"`
	Input json.RawMessage `json:"input"`
}

type anthropicMessagesResponse struct {
	Role       string               `json:"role"`
	Content    []anthropicRespBlock `json:"content"`
	StopReason string               `json:"stop_reason"`
}

type anthropicErrDetail struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

type anthropicErrBody struct {
	Error anthropicErrDetail `json:"error"`
}

// decodeAnthropicSuccess parses a 2xx Messages body: text blocks concatenate to
// Message.Content and tool_use blocks become ToolCalls; stop_reason maps to
// FinishReason.
func decodeAnthropicSuccess(rawBody []byte, status int) (*NormalizedResponse, error) {
	var body anthropicMessagesResponse
	if err := json.Unmarshal(rawBody, &body); err != nil {
		return nil, fmt.Errorf("execution: anthropic transport: decode response: %w", err)
	}
	var text string
	var toolCalls []ToolCall
	// if/else, not a switch on string-literal cases: this package's
	// noslugswitch check forbids that syntactic shape unconditionally.
	for _, b := range body.Content {
		if b.Type == "text" {
			text += b.Text
			continue
		}
		if b.Type == "tool_use" {
			toolCalls = append(toolCalls, ToolCall{Name: b.Name, ArgumentsJSON: string(b.Input)})
		}
	}
	role := body.Role
	if role == "" {
		role = "assistant"
	}
	return &NormalizedResponse{
		Message:      Message{Role: role, Content: text},
		ToolCalls:    toolCalls,
		HTTPStatus:   status,
		FinishReason: body.StopReason,
	}, nil
}

// newAnthropicHTTPError parses a non-2xx Messages error body into the shared
// transport error. The provider text is carried only as the (probe-path)
// message; it never enters Error().
func newAnthropicHTTPError(status int, rawBody []byte, headers http.Header) *nativeOAuthHTTPError {
	var errBody anthropicErrBody
	_ = json.Unmarshal(rawBody, &errBody)
	return &nativeOAuthHTTPError{
		status:  status,
		code:    errBody.Error.Type,
		message: errBody.Error.Message,
		headers: headers.Clone(),
	}
}

// anthropicStreamEvent is the subset of an Anthropic SSE data payload the
// stream runner reads across its several event types.
type anthropicStreamEvent struct {
	Type  string `json:"type"`
	Delta struct {
		Type       string `json:"type"` // "text_delta" on content_block_delta
		Text       string `json:"text"`
		StopReason string `json:"stop_reason"` // on message_delta
	} `json:"delta"`
	Error *anthropicErrDetail `json:"error"`
}

// parseAnthropicSSEData decodes one "data: ..." Anthropic SSE payload into a
// delta, a done flag, and a mid-stream error. An "error" event becomes err (so
// the stream surfaces Chunk{Err}, never a silent truncation); message_stop is
// the terminal done signal.
func parseAnthropicSSEData(data string) (delta string, done bool, err error) {
	var ev anthropicStreamEvent
	if jsonErr := json.Unmarshal([]byte(data), &ev); jsonErr != nil {
		return "", false, fmt.Errorf("execution: anthropic transport: decode stream chunk: %w", jsonErr)
	}
	// if/else, not a switch on string-literal cases (noslugswitch).
	if ev.Type == "error" {
		msg := "provider stream error"
		if ev.Error != nil && ev.Error.Type != "" {
			msg = ev.Error.Type
		}
		return "", false, fmt.Errorf("execution: anthropic transport: stream error: %s", msg)
	}
	if ev.Type == "content_block_delta" {
		if ev.Delta.Type == "text_delta" {
			return ev.Delta.Text, false, nil
		}
		return "", false, nil
	}
	if ev.Type == "message_stop" {
		return "", true, nil
	}
	return "", false, nil
}

// runAnthropicSSE is the streaming goroutine for the Anthropic schema. It
// mirrors runGeminiSSE's timer/cancel discipline but parses Anthropic's event
// stream: text_delta events become Delta chunks, message_stop ends the stream,
// and an error event surfaces as Chunk{Err}.
func runAnthropicSSE(
	streamCtx context.Context,
	cancel context.CancelFunc,
	resp *http.Response,
	requestID string,
	ch chan<- Chunk,
	inflights *inflightRegistry,
	firstByteTimeout, idleGapTimeout time.Duration,
) {
	defer func() {
		inflights.unregister(requestID)
		_ = resp.Body.Close()
		cancel()
		close(ch)
	}()

	lineCh := sseScanner(streamCtx, resp.Body)
	firstByteTimer := time.NewTimer(firstByteTimeout)
	defer firstByteTimer.Stop()
	idleTimer := time.NewTimer(idleGapTimeout)
	defer idleTimer.Stop()
	firstByteSeen := false

	for {
		select {
		case <-streamCtx.Done():
			return
		case <-firstByteTimer.C:
			if !firstByteSeen {
				trySendChunk(ch, streamCtx, Chunk{Err: ErrStreamFirstByteTimeout})
				return
			}
		case <-idleTimer.C:
			trySendChunk(ch, streamCtx, Chunk{Err: ErrStreamIdleGapTimeout})
			return
		case ev, ok := <-lineCh:
			if !ok {
				// Natural EOF: Anthropic closes after message_stop.
				trySendChunk(ch, streamCtx, Chunk{Done: true})
				return
			}
			if ev.err != nil {
				trySendChunk(ch, streamCtx, Chunk{Err: fmt.Errorf("%w: %v", ErrTransportNetwork, ev.err)})
				return
			}
			if ev.line != "" {
				if !firstByteSeen {
					firstByteSeen = true
					firstByteTimer.Stop()
				}
				resetTimer(idleTimer, idleGapTimeout)
			}
			if !strings.HasPrefix(ev.line, "data: ") {
				continue
			}
			delta, done, parseErr := parseAnthropicSSEData(strings.TrimPrefix(ev.line, "data: "))
			if parseErr != nil {
				trySendChunk(ch, streamCtx, Chunk{Err: parseErr})
				return
			}
			if delta != "" {
				if !trySendChunk(ch, streamCtx, Chunk{Delta: delta}) {
					return
				}
			}
			if done {
				trySendChunk(ch, streamCtx, Chunk{Done: true})
				return
			}
		}
	}
}

// trySendChunk sends c on ch unless streamCtx is done first; it reports whether
// the send happened. Extracted so the SSE runners stay within the gocyclo limit.
func trySendChunk(ch chan<- Chunk, streamCtx context.Context, c Chunk) bool {
	select {
	case ch <- c:
		return true
	case <-streamCtx.Done():
		return false
	}
}
