package httpapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/routing"
)

// venomExtError is a typed parse/validation failure of the venom request
// extension (05 §1b). It NAMES the offending field and wraps
// routing.ErrInvalidExtension so the recognized-routing-error mapping treats it
// as venom_invalid_extension (400) — while the handler renders a message that
// includes Field, so the client learns which field was wrong (never a silent
// coercion). Field is a client-supplied structural name, never a secret.
type venomExtError struct {
	Field string
	cause error
}

func (e *venomExtError) Error() string {
	return fmt.Sprintf("routing: invalid venom extension: %s: %v", e.Field, e.cause)
}

// Unwrap reports routing.ErrInvalidExtension so errors.Is / RoutingErrorFor
// recognize this as the invalid-extension code.
func (e *venomExtError) Unwrap() error { return routing.ErrInvalidExtension }

// venomExtIn is the strict wire shape of the venom sub-object. DisallowUnknownFields
// is applied to THIS decode ONLY (never the whole body), so an unknown field
// inside venom is rejected while an unknown TOP-LEVEL OpenAI field stays ignored
// (SDK parity).
type venomExtIn struct {
	ThinkingBudget       *string  `json:"thinking_budget"`
	RequiredCapabilities []string `json:"required_capabilities"`
}

// parseVenomExtension parses the optional top-level "venom" object into a
// routing.VenomExtension. It returns (nil, nil) when the object is absent or
// JSON null. Every failure is a *venomExtError naming the offending field:
//   - an unknown field inside venom (via DisallowUnknownFields);
//   - an invalid thinking_budget value (via routing.ParseThinkingLevel);
//   - an unknown/invalid capability id (via routing.ParseCapability).
//
// It never coerces an invalid value to a default and never silently drops a
// field.
func parseVenomExtension(raw json.RawMessage) (*routing.VenomExtension, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || string(trimmed) == "null" {
		return nil, nil
	}

	dec := json.NewDecoder(bytes.NewReader(trimmed))
	dec.DisallowUnknownFields()
	var in venomExtIn
	if err := dec.Decode(&in); err != nil {
		return nil, &venomExtError{Field: unknownFieldName(err), cause: err}
	}

	ext := &routing.VenomExtension{}
	if in.ThinkingBudget != nil {
		level, err := routing.ParseThinkingLevel(*in.ThinkingBudget)
		if err != nil {
			return nil, &venomExtError{Field: "thinking_budget", cause: err}
		}
		ext.ThinkingBudget = &level
	}
	for _, c := range in.RequiredCapabilities {
		capability, err := routing.ParseCapability(c)
		if err != nil {
			return nil, &venomExtError{Field: "required_capabilities", cause: err}
		}
		ext.RequiredCapabilities = append(ext.RequiredCapabilities, capability)
	}
	return ext, nil
}

// unknownFieldName extracts the field name from encoding/json's
// DisallowUnknownFields error (`json: unknown field "xyz"`). If the error is a
// different decode failure (bad type, malformed JSON), it returns the generic
// "venom" so the message still points the client at the sub-object.
func unknownFieldName(err error) string {
	const prefix = `json: unknown field "`
	msg := err.Error()
	if strings.HasPrefix(msg, prefix) {
		return strings.TrimSuffix(strings.TrimPrefix(msg, prefix), `"`)
	}
	return "venom"
}

// venomExtErrorMessage renders the public, secret-free message for a venom
// extension error: the fixed invalid-extension message plus the offending field
// name. It falls back to the fixed message alone if err is not a *venomExtError.
func venomExtErrorMessage(err error) string {
	var ve *venomExtError
	if errors.As(err, &ve) && ve.Field != "" {
		return msgInvalidExtension + ": " + ve.Field
	}
	return msgInvalidExtension
}
