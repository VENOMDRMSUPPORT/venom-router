package httpapi

// opencode_zen_usability_outcome_test.go pins the bridge from a chat-usability
// verdict to the intelligence ProbeOutcome the CertificationDriver consumes.
// The bridge reuses the EXISTING probe-signal taxonomy (ClassifyProbeSignal)
// rather than inventing a parallel one, so a usability verdict drives the same
// certified/suspended/retry machinery every other probe does.

import (
	"testing"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/intelligence"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/models"
)

func TestZenUsabilityProbeOutcome(t *testing.T) {
	tests := []struct {
		name           string
		verdict        zenChatUsability
		wantDefinitive bool
		wantTruth      models.CapabilityTruth
		wantExecution  intelligence.ProbeExecution
		wantReschedule bool
	}{
		{
			name: "usable certifies chat as supported",
			verdict:        zenChatUsable,
			wantDefinitive: true,
			wantTruth:      models.TruthSupported,
			wantExecution:  intelligence.ProbeSucceeded,
			wantReschedule: false,
		},
		{
			name: "paid model is a definitive unsupported verdict",
			verdict:        zenChatPaidUnusable,
			wantDefinitive: true,
			wantTruth:      models.TruthUnsupported,
			wantExecution:  intelligence.ProbeSucceeded,
			wantReschedule: false,
		},
		{
			name: "free-exhausted is retryable, never a permanent verdict",
			verdict:        zenChatFreeExhausted,
			wantDefinitive: false,
			wantTruth:      models.TruthUnknown,
			wantExecution:  intelligence.ProbeRetryableFailure,
			wantReschedule: true,
		},
		{
			name: "auth failure is a terminal credential block, not a model verdict",
			verdict:        zenChatAuthFailure,
			wantDefinitive: false,
			wantTruth:      models.TruthUnknown,
			wantExecution:  intelligence.ProbeTerminalFailure,
			wantReschedule: false,
		},
		{
			name: "inconclusive establishes nothing",
			verdict:        zenChatInconclusive,
			wantDefinitive: false,
			wantTruth:      models.TruthUnknown,
			wantExecution:  intelligence.ProbeInconclusive,
			wantReschedule: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := zenUsabilityProbeOutcome(tc.verdict)
			if err != nil {
				t.Fatalf("zenUsabilityProbeOutcome(%v) error = %v", tc.verdict, err)
			}
			if got.Definitive != tc.wantDefinitive {
				t.Errorf("Definitive = %v, want %v", got.Definitive, tc.wantDefinitive)
			}
			if got.Truth != tc.wantTruth {
				t.Errorf("Truth = %v, want %v", got.Truth, tc.wantTruth)
			}
			if got.Execution != tc.wantExecution {
				t.Errorf("Execution = %v, want %v", got.Execution, tc.wantExecution)
			}
			if got.Reschedule != tc.wantReschedule {
				t.Errorf("Reschedule = %v, want %v", got.Reschedule, tc.wantReschedule)
			}
		})
	}
}
