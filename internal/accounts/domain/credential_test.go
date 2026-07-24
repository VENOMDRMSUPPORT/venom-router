package domain

import (
	"errors"
	"testing"
	"time"
)

func TestCanAddActiveCredential_RejectsSecondActiveOfSameKind(t *testing.T) {
	existing := []Credential{
		{Kind: CredentialKindAPIKey, State: CredentialActive},
	}

	if err := CanAddActiveCredential(existing, CredentialKindAPIKey); !errors.Is(err, ErrCredentialActiveConflict) {
		t.Fatalf("second active credential of the same kind: err = %v, want ErrCredentialActiveConflict", err)
	}
}

func TestCanAddActiveCredential_AllowsDifferentKindCoexistence(t *testing.T) {
	existing := []Credential{
		{Kind: CredentialKindGitHubOAuth, State: CredentialActive},
	}

	if err := CanAddActiveCredential(existing, CredentialKindCopilotService); err != nil {
		t.Fatalf("active credential of a different kind: err = %v, want nil (multi-kind coexistence)", err)
	}
}

func TestCanAddActiveCredential_RetiredNeverConflicts(t *testing.T) {
	existing := []Credential{
		{Kind: CredentialKindAPIKey, State: CredentialRetired},
	}

	if err := CanAddActiveCredential(existing, CredentialKindAPIKey); err != nil {
		t.Fatalf("active credential after prior retired of the same kind: err = %v, want nil (retired never conflicts)", err)
	}
}

func TestCanStageCredential_RejectsSecondStagedOfSameKind(t *testing.T) {
	existing := []Credential{
		{Kind: CredentialKindAPIKey, State: CredentialStaged},
	}

	if err := CanStageCredential(existing, CredentialKindAPIKey); !errors.Is(err, ErrReauthenticationInProgress) {
		t.Fatalf("second staged credential of the same kind: err = %v, want ErrReauthenticationInProgress", err)
	}
}

func TestCanStageCredential_AllowsDifferentKind(t *testing.T) {
	existing := []Credential{
		{Kind: CredentialKindAPIKey, State: CredentialStaged},
	}

	if err := CanStageCredential(existing, CredentialKindOAuth2); err != nil {
		t.Fatalf("staged credential of a different kind: err = %v, want nil", err)
	}
}

func TestCanStageCredential_ActiveAndStagedOfSameKindCoexist(t *testing.T) {
	existing := []Credential{
		{Kind: CredentialKindGitHubOAuth, State: CredentialActive},
	}

	if err := CanStageCredential(existing, CredentialKindGitHubOAuth); err != nil {
		t.Fatalf("staging alongside an existing active credential of the same kind: err = %v, want nil (active+staged coexist during reauth)", err)
	}
}

func TestCanStageCredential_RetiredNeverConflicts(t *testing.T) {
	existing := []Credential{
		{Kind: CredentialKindAPIKey, State: CredentialRetired},
	}

	if err := CanStageCredential(existing, CredentialKindAPIKey); err != nil {
		t.Fatalf("staging after prior retired of the same kind: err = %v, want nil (retired never conflicts)", err)
	}
}

func TestSwapStagedToActive_PromotesAndRetiresPriorActive(t *testing.T) {
	active := &Credential{ID: "old", Kind: CredentialKindAPIKey, State: CredentialActive}
	staged := Credential{ID: "new", Kind: CredentialKindAPIKey, State: CredentialStaged}

	result, err := SwapStagedToActive(active, staged, "tx-1", fixedNow)
	if err != nil {
		t.Fatalf("SwapStagedToActive: unexpected error: %v", err)
	}
	if !result.Applied {
		t.Fatalf("Applied = false, want true (first application of the swap)")
	}
	if result.RetiredCredential == nil {
		t.Fatalf("RetiredCredential = nil, want the retired old credential")
	}
	if result.RetiredCredential.State != CredentialRetired {
		t.Fatalf("RetiredCredential.State = %s, want retired", result.RetiredCredential.State)
	}
	if result.RetiredCredential.RetiredAt == nil || !result.RetiredCredential.RetiredAt.Equal(fixedNow) {
		t.Fatalf("RetiredCredential.RetiredAt = %v, want %v", result.RetiredCredential.RetiredAt, fixedNow)
	}
	if result.PromotedCredential.State != CredentialActive {
		t.Fatalf("PromotedCredential.State = %s, want active", result.PromotedCredential.State)
	}
	if result.PromotedCredential.ID != "new" {
		t.Fatalf("PromotedCredential.ID = %s, want %q", result.PromotedCredential.ID, "new")
	}
	// The original *Credential input must not be mutated in place.
	if active.State != CredentialActive {
		t.Fatalf("original active input was mutated: State = %s, want unchanged active", active.State)
	}
}

func TestSwapStagedToActive_NoPriorActive(t *testing.T) {
	staged := Credential{ID: "new", Kind: CredentialKindGitHubOAuth, State: CredentialStaged}

	result, err := SwapStagedToActive(nil, staged, "tx-2", fixedNow)
	if err != nil {
		t.Fatalf("SwapStagedToActive: unexpected error: %v", err)
	}
	if !result.Applied {
		t.Fatalf("Applied = false, want true")
	}
	if result.RetiredCredential != nil {
		t.Fatalf("RetiredCredential = %+v, want nil (no prior active credential)", result.RetiredCredential)
	}
	if result.PromotedCredential.State != CredentialActive {
		t.Fatalf("PromotedCredential.State = %s, want active", result.PromotedCredential.State)
	}
}

func TestSwapStagedToActive_RejectsNonStagedCandidate(t *testing.T) {
	notStaged := Credential{ID: "new", Kind: CredentialKindAPIKey, State: CredentialActive}

	_, err := SwapStagedToActive(nil, notStaged, "tx-3", fixedNow)
	if !errors.Is(err, ErrCandidateNotStaged) {
		t.Fatalf("SwapStagedToActive with a non-staged candidate: err = %v, want ErrCandidateNotStaged", err)
	}
}

// TestSwapStagedToActive_IdempotentNoOpOnReplay simulates calling the
// swap again after it already committed: the caller's "active" reference
// is already retired (from the earlier successful application), so the
// replay must be a no-op — not a re-retire, not a double-promote, not an
// error.
func TestSwapStagedToActive_IdempotentNoOpOnReplay(t *testing.T) {
	firstRetiredAt := fixedNow
	alreadyRetired := &Credential{ID: "old", Kind: CredentialKindAPIKey, State: CredentialRetired, RetiredAt: &firstRetiredAt}
	staged := Credential{ID: "new", Kind: CredentialKindAPIKey, State: CredentialStaged}

	replayTime := fixedNow.Add(time.Hour)
	result, err := SwapStagedToActive(alreadyRetired, staged, "tx-1", replayTime)
	if err != nil {
		t.Fatalf("SwapStagedToActive on replay: unexpected error: %v", err)
	}
	if result.Applied {
		t.Fatalf("Applied = true on replay, want false (idempotent no-op)")
	}
	if result.RetiredCredential != nil {
		t.Fatalf("RetiredCredential = %+v on replay, want nil (nothing re-retired)", result.RetiredCredential)
	}
	// The already-retired credential's RetiredAt must not be overwritten
	// with the replay's timestamp.
	if alreadyRetired.RetiredAt == nil || !alreadyRetired.RetiredAt.Equal(firstRetiredAt) {
		t.Fatalf("already-retired credential's RetiredAt changed on replay: got %v, want unchanged %v", alreadyRetired.RetiredAt, firstRetiredAt)
	}
}
