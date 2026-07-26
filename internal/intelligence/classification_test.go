package intelligence

import (
	"errors"
	"testing"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/models"
)

func TestClassify_ImageGenerationOnly_IsCatalogOnly(t *testing.T) {
	res := Classify([]models.Operation{models.OperationImageGeneration}, nil)
	if res.Classification != ClassificationCatalogOnly {
		t.Fatalf("Classification = %s, want catalog_only", res.Classification)
	}
	if res.RoutingCandidate {
		t.Fatal("RoutingCandidate = true, want false for a media-only offering")
	}
	if res.Reason == nil || res.Reason.Kind != ExclusionInformational {
		t.Fatalf("Reason = %+v, want a non-nil informational reason", res.Reason)
	}
}

// TestClassify_EmbeddingsOnly_IsCatalogOnly covers a media-only modality
// (embedding) that has no corresponding entry in Unit 1's fixed
// Operation vocabulary — so the offering's operation set is empty, and
// classification must fall back to the native modalities to prove
// media-only rather than mistaking it for the unknown/empty case.
func TestClassify_EmbeddingsOnly_IsCatalogOnly(t *testing.T) {
	res := Classify(nil, []string{"embedding"})
	if res.Classification != ClassificationCatalogOnly {
		t.Fatalf("Classification = %s, want catalog_only", res.Classification)
	}
	if res.RoutingCandidate {
		t.Fatal("RoutingCandidate = true, want false")
	}
}

func TestClassify_ChatPlusImageGeneration_IsNotCatalogOnly(t *testing.T) {
	res := Classify([]models.Operation{models.OperationChat, models.OperationImageGeneration}, nil)
	if res.Classification == ClassificationCatalogOnly {
		t.Fatal("an offering exposing chat must never be catalog_only")
	}
	if !res.RoutingCandidate {
		t.Fatal("RoutingCandidate = false, want true: chat alongside image_generation is still a routing candidate")
	}
}

// TestClassify_UnknownEmptySet_IsNotMislabelledAsMediaOnly proves an
// empty operation set AND unknown modalities is distinguishable from a
// proven media-only offering: it fails closed to "not a routing
// candidate" without being classified as catalog_only.
func TestClassify_UnknownEmptySet_IsNotMislabelledAsMediaOnly(t *testing.T) {
	res := Classify(nil, nil)
	if res.Classification == ClassificationCatalogOnly {
		t.Fatal("an empty/unknown operation set must never be classified as catalog_only")
	}
	if res.RoutingCandidate {
		t.Fatal("RoutingCandidate = true, want false: unknown must fail closed to not-a-routing-candidate")
	}
	if res.Classification != ClassificationUnclassified {
		t.Fatalf("Classification = %s, want unclassified", res.Classification)
	}
	if res.Reason == nil || res.Reason.Code != ReasonCodeNoOperations {
		t.Fatalf("Reason = %+v, want reason code %s", res.Reason, ReasonCodeNoOperations)
	}
}

func TestClassify_NonChatNonMediaOperations_IsRoutingCandidate(t *testing.T) {
	// vision + tools, no chat, no image_generation: not proven media-only,
	// so it remains a routing candidate (those operations are
	// independently certifiable/routable).
	res := Classify([]models.Operation{models.OperationVision, models.OperationTools}, nil)
	if res.Classification == ClassificationCatalogOnly {
		t.Fatal("non-chat, non-media operations must not be classified as catalog_only")
	}
	if !res.RoutingCandidate {
		t.Fatal("RoutingCandidate = false, want true")
	}
}

func TestExclusionReason_CatalogOnlyIsInformationalNeverFailure(t *testing.T) {
	res := Classify([]models.Operation{models.OperationImageGeneration}, nil)
	if res.Reason == nil {
		t.Fatal("expected a non-nil exclusion reason")
	}
	if res.Reason.Kind == ExclusionFailure {
		t.Fatal("catalog_only exclusion must never be classified as a failure kind")
	}
	if res.Reason.Kind != ExclusionInformational {
		t.Fatalf("Reason.Kind = %s, want informational", res.Reason.Kind)
	}
}

func TestModelsPackage_StillEnumeratesExactlySixStatesAndRejectsCatalogOnly(t *testing.T) {
	// A cross-package guard: this unit must never add catalog_only as a
	// seventh certification state in internal/models.
	states := models.CertificationStates()
	if len(states) != 6 {
		t.Fatalf("models.CertificationStates() has %d entries, want exactly 6", len(states))
	}
	if _, err := models.ParseCertificationState("catalog_only"); !errors.Is(err, models.ErrUnknownCertificationState) {
		t.Fatalf(`models.ParseCertificationState("catalog_only") error = %v, want ErrUnknownCertificationState`, err)
	}
}

func TestReclassify_CatalogOnlyIsTerminal(t *testing.T) {
	// Once catalog_only, no re-derivation — even from evidence that would
	// normally derive routable_candidate — may move it back.
	_, err := Reclassify(ClassificationCatalogOnly, []models.Operation{models.OperationChat}, nil)
	if !errors.Is(err, ErrCatalogOnlyIsTerminal) {
		t.Fatalf("Reclassify from catalog_only to a chat-derived result error = %v, want ErrCatalogOnlyIsTerminal", err)
	}
}

func TestReclassify_NonTerminalPreviousStateReclassifiesFreely(t *testing.T) {
	res, err := Reclassify(ClassificationUnclassified, []models.Operation{models.OperationChat}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Classification != ClassificationRoutableCandidate {
		t.Fatalf("Classification = %s, want routable_candidate", res.Classification)
	}
}
