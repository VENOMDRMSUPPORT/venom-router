package providers

// This file holds the ONE mapping from the 3-way credential classification
// (ValidateAPIKey's ValidationStatus) onto a HealthObservation, shared by every
// adapter whose health check IS its authentic validation call — agnes-ai,
// nvidia-nim and gemini-cli today. Before it existed each of those adapters
// carried its own copy of the same switch, which is three places for the
// mapping to drift and three places to test.
//
// opencode-zen deliberately keeps its own copy: its file is shipped and tested,
// and its classification carries a provider-specific billing quirk
// (ErrProbeAuthenticated) that is not part of this general mapping.

// observationFromValidation maps a credential classification onto the account
// health axis (types.go's documented correspondence) and stamps checkedAt on
// EVERY outcome:
//
//	ValidationValid       -> healthy     (the provider authenticated the credential)
//	ValidationInvalid     -> expired     (the provider READ and REJECTED it; transport fine)
//	ValidationUnavailable -> unreachable (retryable; says nothing about the credential)
//
// checkedAt is passed in rather than read from a clock here so the caller's
// injected clock remains the single source of time. It is never omitted: an
// unstamped observation carries CheckedAt == 0, which any consumer reads as a
// real 1970-01-01 timestamp — the 0-as-unknown fabrication this project rejects
// everywhere else.
func observationFromValidation(status ValidationStatus, scope string, checkedAt int64) HealthObservation {
	switch status {
	case ValidationValid:
		return HealthObservation{
			Status: "healthy", Scope: scope,
			CredentialValid: true, TransportReachable: true,
			CheckedAt: checkedAt,
		}
	case ValidationInvalid:
		return HealthObservation{
			Status: "expired", Scope: scope,
			CredentialValid: false, TransportReachable: true,
			CheckedAt: checkedAt,
			Failure: &HealthFailure{
				Class:       "auth",
				Retryable:   false,
				SafeMessage: "provider rejected the credential (401/403)",
			},
		}
	default:
		return HealthObservation{
			Status: "unreachable", Scope: scope,
			CredentialValid: false, TransportReachable: false,
			CheckedAt: checkedAt,
			Failure: &HealthFailure{
				Class:       "unavailable",
				Retryable:   true,
				SafeMessage: "provider unavailable or rate limited",
			},
		}
	}
}
