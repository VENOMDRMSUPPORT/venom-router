package execution

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// This file holds the Gemini (Google generativelanguage) request/response
// wire mapping shared by BOTH Gemini transports: NativeOAuthTransport
// (native_oauth, Authorization: Bearer — antigravity) and NativeAPITransport
// (native_api, x-goog-api-key — gemini-cli). The two transports differ ONLY
// in how they authenticate the HTTP request; everything about the Gemini
// schema — the request builder, the success decoder, the error body shape,
// and the SSE runner — lives here so the mapping is defined once, not copied
// per transport (P7-EXEC-001).

// Gemini generateContent request/response wire types ----------------------

type geminiPart struct {
	Text         *string           `json:"text,omitempty"`
	InlineData   *geminiInlineData `json:"inlineData,omitempty"`
	FunctionCall *geminiFnCall     `json:"functionCall,omitempty"`
}

// geminiInlineData carries an inline (base64) image for a multimodal part
// (P5-EXEC-004). Gemini's REST inlineData requires the bytes inline — a bare
// image URL is NOT expressible here, so a URL-only image fails closed.
type geminiInlineData struct {
	MimeType string `json:"mimeType"`
	Data     string `json:"data"`
}

type geminiFnCall struct {
	Name string          `json:"name"`
	Args json.RawMessage `json:"args,omitempty"`
}

// geminiFunctionDeclaration is one declared tool (P5-EXEC-004). Parameters is
// embedded raw JSON — the client's schema is forwarded verbatim.
type geminiFunctionDeclaration struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
}

type geminiTool struct {
	FunctionDeclarations []geminiFunctionDeclaration `json:"functionDeclarations"`
}

type geminiContent struct {
	Role  string       `json:"role"`
	Parts []geminiPart `json:"parts"`
}

// geminiSystemInstruction is Gemini's dedicated top-level carrier for system
// text. Google takes system guidance in this separate field, NOT as a turn in
// `contents` — it deliberately has no role. Modelled distinctly from
// geminiContent so a system-less request omits the field entirely (omitempty)
// and stays byte-identical to a pre-fix request on the wire.
type geminiSystemInstruction struct {
	Parts []geminiPart `json:"parts"`
}

type geminiGenerationConfig struct {
	MaxOutputTokens *int `json:"maxOutputTokens,omitempty"`
}

type geminiGenerateReq struct {
	Contents          []geminiContent          `json:"contents"`
	SystemInstruction *geminiSystemInstruction `json:"systemInstruction,omitempty"`
	Tools             []geminiTool             `json:"tools,omitempty"`
	GenerationConfig  *geminiGenerationConfig  `json:"generationConfig,omitempty"`
}

type geminiCandidate struct {
	Content      geminiContent `json:"content"`
	FinishReason string        `json:"finishReason"`
}

type geminiGenerateResp struct {
	Candidates []geminiCandidate `json:"candidates"`
}

type geminiErrDetail struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Status  string `json:"status"`
	Scope   string `json:"scope"`
}

type geminiErrBody struct {
	Error geminiErrDetail `json:"error"`
}

// buildGeminiRequest maps a normalized request onto Gemini's generateContent
// shape, failing CLOSED (ErrRequestFeatureUnsupported) on anything it cannot
// faithfully express — a URL-only image (inlineData needs the bytes), an
// unknown content-part kind, or a tool_choice directive (Gemini's
// functionCallingConfig has no faithful mapping for an arbitrary OpenAI
// tool_choice string). A text-only, system-less request produces exactly the
// pre-P5-EXEC-004 shape. The error names the FEATURE only, never the tool
// description or URL.
//
// System messages are routed into the top-level systemInstruction field
// rather than sent as a `contents` turn: Gemini takes system text separately,
// and geminiRoleFor would otherwise map "system" to "model", making the model
// appear to have SAID the system prompt. Multiple system messages concatenate
// their parts in order. A request with no system message omits
// systemInstruction entirely, so its wire body is byte-identical to before
// this fix.
func buildGeminiRequest(req NormalizedRequest) (geminiGenerateReq, error) {
	if req.ToolChoice != "" {
		return geminiGenerateReq{}, fmt.Errorf("%w: tool_choice", ErrRequestFeatureUnsupported)
	}
	if req.ResponseFormat != "" {
		return geminiGenerateReq{}, fmt.Errorf("%w: response_format", ErrRequestFeatureUnsupported)
	}
	contents := make([]geminiContent, 0, len(req.Messages))
	var systemParts []geminiPart
	for _, m := range req.Messages {
		parts, err := geminiPartsFor(m)
		if err != nil {
			return geminiGenerateReq{}, err
		}
		if m.Role == "system" {
			systemParts = append(systemParts, parts...)
			continue
		}
		contents = append(contents, geminiContent{Role: geminiRoleFor(m.Role), Parts: parts})
	}
	out := geminiGenerateReq{Contents: contents}
	if len(systemParts) > 0 {
		out.SystemInstruction = &geminiSystemInstruction{Parts: systemParts}
	}
	if len(req.Tools) > 0 {
		decls := make([]geminiFunctionDeclaration, 0, len(req.Tools))
		for _, td := range req.Tools {
			d := geminiFunctionDeclaration{Name: td.Name, Description: td.Description}
			if td.ParametersJSON != "" {
				d.Parameters = json.RawMessage(td.ParametersJSON)
			}
			decls = append(decls, d)
		}
		out.Tools = []geminiTool{{FunctionDeclarations: decls}}
	}
	if req.MaxTokens != nil {
		out.GenerationConfig = &geminiGenerationConfig{MaxOutputTokens: req.MaxTokens}
	}
	return out, nil
}

// geminiPartsFor maps one message's content to Gemini parts. A message with no
// Parts keeps the single-text-part shape used before P5-EXEC-004.
func geminiPartsFor(m Message) ([]geminiPart, error) {
	if len(m.Parts) == 0 {
		text := m.Content
		return []geminiPart{{Text: &text}}, nil
	}
	parts := make([]geminiPart, 0, len(m.Parts))
	for _, p := range m.Parts {
		switch p.Kind {
		case ContentPartText:
			text := p.Text
			parts = append(parts, geminiPart{Text: &text})
		case ContentPartImage:
			if p.ImageBase64 == "" || p.MediaType == "" {
				// A bare URL (or an image missing its media type) cannot be
				// expressed as inlineData — fail closed rather than drop it.
				return nil, fmt.Errorf("%w: image part requires inline base64 data and a media type", ErrRequestFeatureUnsupported)
			}
			parts = append(parts, geminiPart{InlineData: &geminiInlineData{MimeType: p.MediaType, Data: p.ImageBase64}})
		default:
			return nil, fmt.Errorf("%w: content part kind %q", ErrRequestFeatureUnsupported, p.Kind)
		}
	}
	return parts, nil
}

// geminiRoleFor maps Venom's role vocabulary onto Gemini's.
func geminiRoleFor(role string) string {
	if role == "user" {
		return "user"
	}
	return "model"
}

// decodeGeminiSuccess parses a 2xx generateContent body into a
// NormalizedResponse: it concatenates the candidate's text parts and lifts any
// functionCall parts into ToolCalls. Shared by both Gemini transports so the
// success decoding is defined once.
func decodeGeminiSuccess(rawBody []byte, status int) (*NormalizedResponse, error) {
	var okBody geminiGenerateResp
	if err := json.Unmarshal(rawBody, &okBody); err != nil {
		return nil, fmt.Errorf("execution: gemini transport: decode response: %w", err)
	}
	if len(okBody.Candidates) == 0 {
		return nil, errors.New("execution: gemini transport: no candidates in response")
	}
	candidate := okBody.Candidates[0]

	var textContent string
	var toolCalls []ToolCall
	for _, part := range candidate.Content.Parts {
		if part.Text != nil {
			textContent += *part.Text
		}
		if part.FunctionCall != nil {
			argsJSON := string(part.FunctionCall.Args)
			toolCalls = append(toolCalls, ToolCall{Name: part.FunctionCall.Name, ArgumentsJSON: argsJSON})
		}
	}

	return &NormalizedResponse{
		Message:      Message{Role: candidate.Content.Role, Content: textContent},
		ToolCalls:    toolCalls,
		HTTPStatus:   status,
		FinishReason: candidate.FinishReason,
	}, nil
}

// parseGeminiSSEData decodes one "data: ..." SSE payload into a delta
// string and a done flag. Branchy JSON + parts-walk logic is isolated
// here so runGeminiSSE stays within the gocyclo limit.
func parseGeminiSSEData(data string) (delta string, done bool, err error) {
	var sc geminiGenerateResp
	if jsonErr := json.Unmarshal([]byte(data), &sc); jsonErr != nil {
		return "", false, fmt.Errorf("execution: gemini transport: decode stream chunk: %w", jsonErr)
	}
	if len(sc.Candidates) == 0 {
		return "", false, nil
	}
	candidate := sc.Candidates[0]
	for _, part := range candidate.Content.Parts {
		if part.Text != nil {
			delta += *part.Text
		}
	}
	done = candidate.FinishReason != "" && candidate.FinishReason != "FINISH_REASON_UNSPECIFIED"
	return delta, done, nil
}

// runGeminiSSE is the shared streaming goroutine body for both Gemini
// transports. Extracting it as a free function (rather than a per-transport
// method) keeps the SSE mapping defined once and keeps each transport's Stream
// within the project's gocyclo limit. inflights, firstByteTimeout and
// idleGapTimeout are the calling transport's own values.
func runGeminiSSE(
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
				select {
				case ch <- Chunk{Err: ErrStreamFirstByteTimeout}:
				case <-streamCtx.Done():
				}
				return
			}

		case <-idleTimer.C:
			select {
			case ch <- Chunk{Err: ErrStreamIdleGapTimeout}:
			case <-streamCtx.Done():
			}
			return

		case ev, ok := <-lineCh:
			if !ok {
				// Natural EOF — Gemini closes the connection when done.
				select {
				case ch <- Chunk{Done: true}:
				case <-streamCtx.Done():
				}
				return
			}
			if ev.err != nil {
				select {
				case ch <- Chunk{Err: fmt.Errorf("%w: %v", ErrTransportNetwork, ev.err)}:
				case <-streamCtx.Done():
				}
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
			delta, done, parseErr := parseGeminiSSEData(strings.TrimPrefix(ev.line, "data: "))
			if parseErr != nil {
				select {
				case ch <- Chunk{Err: parseErr}:
				case <-streamCtx.Done():
				}
				return
			}
			if delta != "" {
				select {
				case ch <- Chunk{Delta: delta}:
				case <-streamCtx.Done():
					return
				}
			}
			if done {
				select {
				case ch <- Chunk{Done: true}:
				case <-streamCtx.Done():
				}
				return
			}
		}
	}
}
