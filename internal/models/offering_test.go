package models

import (
	"errors"
	"testing"
)

func TestParseAvailability_Valid(t *testing.T) {
	for _, s := range []string{"available", "withdrawn", "unknown"} {
		if _, err := ParseAvailability(s); err != nil {
			t.Fatalf("ParseAvailability(%q): unexpected error: %v", s, err)
		}
	}
}

func TestParseAvailability_RejectsUnknownValue(t *testing.T) {
	if _, err := ParseAvailability("deprecated"); !errors.Is(err, ErrUnknownAvailability) {
		t.Fatalf("ParseAvailability(deprecated) error = %v, want ErrUnknownAvailability", err)
	}
}

func TestParseOperation_ValidVocabulary(t *testing.T) {
	valid := []string{
		"chat", "streaming", "tools", "structured_output",
		"vision", "context_window", "image_generation", "reasoning",
	}
	for _, s := range valid {
		if _, err := ParseOperation(s); err != nil {
			t.Fatalf("ParseOperation(%q): unexpected error: %v", s, err)
		}
	}
}

func TestParseOperation_RejectsUnrecognized(t *testing.T) {
	for _, s := range []string{"embeddings", "audio", "translation", "", "Chat"} {
		if _, err := ParseOperation(s); !errors.Is(err, ErrUnknownOperation) {
			t.Fatalf("ParseOperation(%q) error = %v, want ErrUnknownOperation", s, err)
		}
	}
}

func TestParseOperation_Reasoning(t *testing.T) {
	op, err := ParseOperation("reasoning")
	if err != nil {
		t.Fatalf("ParseOperation(\"reasoning\"): unexpected error: %v", err)
	}
	if op != OperationReasoning {
		t.Fatalf("ParseOperation(\"reasoning\") = %q, want %q", op, OperationReasoning)
	}
}

func TestOperations_ContainsReasoningExactlyOnce(t *testing.T) {
	got := Operations()
	count := 0
	for _, op := range got {
		if op == OperationReasoning {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("Operations() contains OperationReasoning %d times, want exactly 1: %v", count, got)
	}
}

func TestOperations_EnumeratesExactlyEight(t *testing.T) {
	got := Operations()
	if len(got) != 8 {
		t.Fatalf("Operations() has %d entries, want exactly 8: %v", len(got), got)
	}
}

func TestNewOfferingOperation_RejectsUnknownOperation(t *testing.T) {
	if _, err := NewOfferingOperation("acct-1", "provider-1", "model-1", "not-a-real-operation"); !errors.Is(err, ErrUnknownOperation) {
		t.Fatalf("expected ErrUnknownOperation, got %v", err)
	}
}

func TestNewOfferingOperation_RejectsEmptyIdentity(t *testing.T) {
	if _, err := NewOfferingOperation("", "provider-1", "model-1", "chat"); err == nil {
		t.Fatal("expected error for empty account ID, got nil")
	}
}

func TestNewOfferingOperation_Accepted(t *testing.T) {
	op, err := NewOfferingOperation("acct-1", "provider-1", "model-1", "chat")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if op.Operation != OperationChat {
		t.Fatalf("Operation = %s, want %s", op.Operation, OperationChat)
	}
}

// TestOffering_DistinctIdentityPerAccount proves the same model on two
// accounts is two distinct offerings (02 §3).
func TestOffering_DistinctIdentityPerAccount(t *testing.T) {
	a := OfferingIdentity{AccountID: "acct-1", ProviderModelID: "model-1"}
	b := OfferingIdentity{AccountID: "acct-2", ProviderModelID: "model-1"}
	if a == b {
		t.Fatal("offering identity must differ across accounts for the same provider_model_id")
	}
}

func TestOffering_UnknownNumericFieldsAreNotZero(t *testing.T) {
	o := Offering{Identity: OfferingIdentity{AccountID: "acct-1", ProviderModelID: "model-1"}}
	if o.ContextLength != nil {
		t.Fatalf("ContextLength = %v, want nil (unknown)", o.ContextLength)
	}

	known := 0
	o.ContextLength = &known
	if o.ContextLength == nil || *o.ContextLength != 0 {
		t.Fatalf("ContextLength = %v, want known zero", o.ContextLength)
	}
}
