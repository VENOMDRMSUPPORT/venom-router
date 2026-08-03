package providers

import (
	"context"
	"errors"
	"testing"
)

// TestObservationFromValidation pins the shared classification -> health axis
// mapping in BOTH directions: each status produces its own distinct state, and
// every outcome carries the caller's CheckedAt stamp (an unstamped observation
// would read as 1970-01-01 to any consumer).
func TestObservationFromValidation(t *testing.T) {
	const checkedAt = int64(1_700_000_000)

	cases := []struct {
		status          ValidationStatus
		wantState       string
		wantCredValid   bool
		wantReachable   bool
		wantFailure     bool
		wantRetryable   bool
		wantFailureKind string
	}{
		{ValidationValid, "healthy", true, true, false, false, ""},
		{ValidationInvalid, "expired", false, true, true, false, "auth"},
		{ValidationUnavailable, "unreachable", false, false, true, true, "unavailable"},
		// An unrecognized status must fall to the retryable state, never healthy.
		{ValidationStatus("something-new"), "unreachable", false, false, true, true, "unavailable"},
	}
	for _, c := range cases {
		obs := observationFromValidation(c.status, "account", checkedAt)
		if obs.Status != c.wantState {
			t.Errorf("%q -> Status = %q, want %q", c.status, obs.Status, c.wantState)
		}
		if obs.CredentialValid != c.wantCredValid || obs.TransportReachable != c.wantReachable {
			t.Errorf("%q -> CredentialValid/TransportReachable = %v/%v, want %v/%v",
				c.status, obs.CredentialValid, obs.TransportReachable, c.wantCredValid, c.wantReachable)
		}
		if obs.CheckedAt != checkedAt {
			t.Errorf("%q -> CheckedAt = %d, want the caller's stamp %d", c.status, obs.CheckedAt, checkedAt)
		}
		if (obs.Failure != nil) != c.wantFailure {
			t.Errorf("%q -> Failure present = %v, want %v", c.status, obs.Failure != nil, c.wantFailure)
		}
		if obs.Failure != nil {
			if obs.Failure.Class != c.wantFailureKind {
				t.Errorf("%q -> Failure.Class = %q, want %q", c.status, obs.Failure.Class, c.wantFailureKind)
			}
			if obs.Failure.Retryable != c.wantRetryable {
				t.Errorf("%q -> Failure.Retryable = %v, want %v", c.status, obs.Failure.Retryable, c.wantRetryable)
			}
		}
	}
}

func TestObservationFromValidation_ScopeIsCarriedThrough(t *testing.T) {
	for _, scope := range []string{"account", "offering"} {
		if got := observationFromValidation(ValidationValid, scope, 1); got.Scope != scope {
			t.Errorf("Scope = %q, want %q", got.Scope, scope)
		}
	}
}

// healthAdapterCase drives one adapter's HealthAdapter surface through all three
// outcomes. Every P7 API-key adapter's health check IS its authentic validation
// call, so the same three-state table applies to each — and each must stamp
// CheckedAt from its injected clock.
type healthAdapterCase struct {
	name    string
	adapter func(status int, err error) HealthAdapter
}

func TestP7Adapters_HealthObservationsAreStampedAndDistinct(t *testing.T) {
	wantCheckedAt := frozenClock()().Unix()

	adapters := []healthAdapterCase{
		{
			name: "agnes-ai",
			adapter: func(status int, err error) HealthAdapter {
				return NewAgnesAIAdapter((&fakeChatProbe{status: status, err: err}).probe, (&fakeModelsProbe{}).probe, frozenClock())
			},
		},
		{
			name: "nvidia-nim",
			adapter: func(status int, err error) HealthAdapter {
				return NewNvidiaNIMAdapter((&fakeChatProbe{status: status, err: err}).probe, (&fakeModelsProbe{}).probe, nil, frozenClock())
			},
		},
		{
			name: "gemini-cli",
			adapter: func(status int, err error) HealthAdapter {
				return NewGeminiCLIAdapter((&fakeGoogleProbe{status: status, err: err}).probe, frozenClock())
			},
		},
		{
			name: "ollama-cloud",
			adapter: func(status int, err error) HealthAdapter {
				id := &fakeOllamaIdentity{status: status, body: ollamaMeOK, err: err}
				return NewOllamaCloudAdapter(id.probe, (&fakeModelsProbe{}).probe, nil, frozenClock())
			},
		},
	}

	outcomes := []struct {
		label     string
		status    int
		err       error
		wantState string
	}{
		{"authenticated", 200, nil, "healthy"},
		{"credential rejected", 401, nil, "expired"},
		{"rate limited", 429, nil, "unreachable"},
		{"transport failure", 0, errors.New("dial tcp: refused"), "unreachable"},
	}

	for _, a := range adapters {
		for _, o := range outcomes {
			obs, err := a.adapter(o.status, o.err).CheckAccountHealth(context.Background(), StoredCredentials{Value: "k"})
			if err != nil {
				t.Fatalf("%s/%s: CheckAccountHealth error = %v, want nil (the outcome IS the observation)", a.name, o.label, err)
			}
			if obs.Status != o.wantState {
				t.Errorf("%s/%s: Status = %q, want %q", a.name, o.label, obs.Status, o.wantState)
			}
			if obs.Scope != "account" {
				t.Errorf("%s/%s: Scope = %q, want account", a.name, o.label, obs.Scope)
			}
			if obs.CheckedAt != wantCheckedAt {
				t.Errorf("%s/%s: CheckedAt = %d, want the injected clock %d — an unstamped observation reads as 1970",
					a.name, o.label, obs.CheckedAt, wantCheckedAt)
			}
		}

		// The offering-scoped call reuses the account probe but must report its
		// own scope, or a per-offering health row would be attributed to the
		// account.
		obs, _ := a.adapter(200, nil).CheckOfferingHealth(context.Background(), StoredCredentials{Value: "k"}, "some-model")
		if obs.Scope != "offering" {
			t.Errorf("%s: CheckOfferingHealth Scope = %q, want offering", a.name, obs.Scope)
		}
		if obs.CheckedAt != wantCheckedAt {
			t.Errorf("%s: CheckOfferingHealth CheckedAt = %d, want %d", a.name, obs.CheckedAt, wantCheckedAt)
		}
	}
}
