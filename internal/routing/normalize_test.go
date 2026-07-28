package routing

import (
	"errors"
	"reflect"
	"testing"
)

// textRequest returns a minimal valid plain-text request that tests
// mutate per case.
func textRequest() Request {
	return Request{
		Parts:                  []MessagePart{{Kind: PartText}},
		EstimatedContextTokens: 1000,
	}
}

// capsOf extracts the derived capability set for compact assertions.
func capsOf(t *testing.T, req Request) []Capability {
	t.Helper()
	reqs, err := Normalize(req)
	if err != nil {
		t.Fatalf("Normalize() error = %v, want nil", err)
	}
	return reqs.Capabilities
}

// TestNormalize_CapabilityAndModalityExtraction is the Step-1 extraction
// table: each request-shape signal derives exactly its capability, and a
// plain text request derives none of them.
func TestNormalize_CapabilityAndModalityExtraction(t *testing.T) {
	cases := []struct {
		name     string
		mutate   func(*Request)
		wantCaps []Capability
		wantText bool
		wantVis  bool
		wantDoc  bool
	}{
		{
			name:     "plain text derives no capabilities",
			mutate:   func(r *Request) {},
			wantCaps: nil,
			wantText: true,
		},
		{
			name:     "image part derives vision + vision modality",
			mutate:   func(r *Request) { r.Parts = append(r.Parts, MessagePart{Kind: PartImage}) },
			wantCaps: []Capability{CapabilityVision},
			wantText: true,
			wantVis:  true,
		},
		{
			name:     "tools present derives tools",
			mutate:   func(r *Request) { r.ToolsPresent = true },
			wantCaps: []Capability{CapabilityTools},
			wantText: true,
		},
		{
			name:     "response_format derives structured_output",
			mutate:   func(r *Request) { r.StructuredOutputRequested = true },
			wantCaps: []Capability{CapabilityStructuredOutput},
			wantText: true,
		},
		{
			name:     "stream flag derives streaming",
			mutate:   func(r *Request) { r.Stream = true },
			wantCaps: []Capability{CapabilityStreaming},
			wantText: true,
		},
		{
			name:     "document part derives documents modality, no capability",
			mutate:   func(r *Request) { r.Parts = append(r.Parts, MessagePart{Kind: PartDocument}) },
			wantCaps: nil,
			wantText: true,
			wantDoc:  true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := textRequest()
			tc.mutate(&req)
			reqs, err := Normalize(req)
			if err != nil {
				t.Fatalf("Normalize() error = %v, want nil", err)
			}
			if !reflect.DeepEqual(reqs.Capabilities, tc.wantCaps) {
				t.Errorf("Capabilities = %v, want %v", reqs.Capabilities, tc.wantCaps)
			}
			if reqs.TextModality != tc.wantText || reqs.VisionModality != tc.wantVis || reqs.DocumentModality != tc.wantDoc {
				t.Errorf("modalities = text %v / vision %v / documents %v, want %v / %v / %v",
					reqs.TextModality, reqs.VisionModality, reqs.DocumentModality, tc.wantText, tc.wantVis, tc.wantDoc)
			}
			if reqs.ContextTokens != req.EstimatedContextTokens {
				t.Errorf("ContextTokens = %d, want %d (S passes through)", reqs.ContextTokens, req.EstimatedContextTokens)
			}
		})
	}
}

// TestNormalize_UnionWithExplicitSet proves the §2 Step 1 union: inferred
// {vision} ∪ explicit {reasoning, vision} = {reasoning, vision}, sorted
// and deduplicated — and that an explicit set never gates an inferred
// capability away.
func TestNormalize_UnionWithExplicitSet(t *testing.T) {
	req := textRequest()
	req.Parts = append(req.Parts, MessagePart{Kind: PartImage})
	req.Venom = &VenomExtension{
		RequiredCapabilities: []Capability{CapabilityReasoning, CapabilityVision},
	}
	want := []Capability{CapabilityReasoning, CapabilityVision}
	if got := capsOf(t, req); !reflect.DeepEqual(got, want) {
		t.Fatalf("union = %v, want %v (sorted, deduplicated)", got, want)
	}

	// Explicit alone gates nothing away: streaming stays inferred even
	// though the explicit set does not mention it.
	req2 := textRequest()
	req2.Stream = true
	req2.Venom = &VenomExtension{RequiredCapabilities: []Capability{CapabilityReasoning}}
	want2 := []Capability{CapabilityReasoning, CapabilityStreaming}
	if got := capsOf(t, req2); !reflect.DeepEqual(got, want2) {
		t.Fatalf("union = %v, want %v (inferred streaming preserved)", got, want2)
	}
}

// TestNormalize_Deterministic proves the same request normalizes to a
// deep-equal Requirements twice, and a permuted (and duplicated) explicit
// list yields the identical sorted output.
func TestNormalize_Deterministic(t *testing.T) {
	build := func(explicit []Capability) Request {
		req := textRequest()
		req.Parts = append(req.Parts, MessagePart{Kind: PartImage})
		req.Stream = true
		level := ThinkingExtended
		req.Venom = &VenomExtension{
			ThinkingBudget:       &level,
			RequiredCapabilities: explicit,
		}
		return req
	}

	first, err := Normalize(build([]Capability{CapabilityReasoning, CapabilityTools, CapabilityVision}))
	if err != nil {
		t.Fatalf("Normalize() error = %v, want nil", err)
	}
	second, err := Normalize(build([]Capability{CapabilityReasoning, CapabilityTools, CapabilityVision}))
	if err != nil {
		t.Fatalf("Normalize() error = %v, want nil", err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("same request normalized twice differs:\n%+v\n%+v", first, second)
	}

	permuted, err := Normalize(build([]Capability{CapabilityVision, CapabilityReasoning, CapabilityTools, CapabilityReasoning}))
	if err != nil {
		t.Fatalf("Normalize() error = %v, want nil", err)
	}
	if !reflect.DeepEqual(first.Capabilities, permuted.Capabilities) {
		t.Fatalf("permuted+duplicated explicit list changed the output: %v vs %v", first.Capabilities, permuted.Capabilities)
	}
}

// TestNormalize_RejectsInvalidInput proves the fail-closed direction:
// an unknown capability identifier, an invalid thinking level, an
// unknown message-part kind, and a non-positive S each produce a
// distinct typed error — never a silent coercion (05 §1b).
func TestNormalize_RejectsInvalidInput(t *testing.T) {
	badCap := textRequest()
	badCap.Venom = &VenomExtension{RequiredCapabilities: []Capability{"magic"}}
	if _, err := Normalize(badCap); !errors.Is(err, ErrInvalidExtension) || !errors.Is(err, ErrUnknownCapability) {
		t.Fatalf("unknown capability error = %v, want ErrInvalidExtension wrapping ErrUnknownCapability", err)
	}

	badLevel := textRequest()
	level := ThinkingLevel("hyper")
	badLevel.Venom = &VenomExtension{ThinkingBudget: &level}
	if _, err := Normalize(badLevel); !errors.Is(err, ErrInvalidExtension) || !errors.Is(err, ErrUnknownThinkingLevel) {
		t.Fatalf("invalid thinking level error = %v, want ErrInvalidExtension wrapping ErrUnknownThinkingLevel", err)
	}

	badPart := textRequest()
	badPart.Parts = append(badPart.Parts, MessagePart{Kind: PartKind("audio")})
	if _, err := Normalize(badPart); !errors.Is(err, ErrUnknownPartKind) {
		t.Fatalf("unknown part kind error = %v, want ErrUnknownPartKind", err)
	}

	for _, s := range []int64{0, -1} {
		zeroS := textRequest()
		zeroS.EstimatedContextTokens = s
		if _, err := Normalize(zeroS); !errors.Is(err, ErrInvalidContextNeed) {
			t.Fatalf("S=%d error = %v, want ErrInvalidContextNeed", s, err)
		}
	}
}

// TestNormalize_ThinkingLevelPassThrough proves a valid requested level
// is carried through (as an independent copy) and a nil level stays nil —
// the tier default applies later (UNIT 4), never here.
func TestNormalize_ThinkingLevelPassThrough(t *testing.T) {
	req := textRequest()
	level := ThinkingNone
	req.Venom = &VenomExtension{ThinkingBudget: &level}
	reqs, err := Normalize(req)
	if err != nil {
		t.Fatalf("Normalize() error = %v, want nil", err)
	}
	if reqs.RequestedThinking == nil || *reqs.RequestedThinking != ThinkingNone {
		t.Fatalf("RequestedThinking = %v, want %q", reqs.RequestedThinking, ThinkingNone)
	}
	if reqs.RequestedThinking == &level {
		t.Fatalf("RequestedThinking aliases the caller's pointer; want an independent copy")
	}

	noExt, err := Normalize(textRequest())
	if err != nil {
		t.Fatalf("Normalize() error = %v, want nil", err)
	}
	if noExt.RequestedThinking != nil {
		t.Fatalf("RequestedThinking = %v with no extension, want nil (tier default applies later)", *noExt.RequestedThinking)
	}
}
