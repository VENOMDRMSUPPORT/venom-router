package httpapi

// enrollment_discovery_test.go pins the auto-discovery-on-connect behaviour: a
// successful API-key enrollment fires a background model discovery for the new
// account (so its models are fetched + verified without a manual "Refresh
// models"), while a FAILED connect fires nothing.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/providers"
)

type fakeDiscoveryTrigger struct {
	calledWith []string
}

func (f *fakeDiscoveryTrigger) TriggerBackgroundDiscovery(_ context.Context, accountID string) {
	f.calledWith = append(f.calledWith, accountID)
}

func TestEnrollment_ValidKey_TriggersBackgroundDiscovery(t *testing.T) {
	const canaryKey = "good-key-CANARY-trigger-9xQ2"
	adapter := newFakeAPIKeyAdapter()
	adapter.validKey = canaryKey
	adapter.creds = providers.StoredCredentials{Value: canaryKey}

	h, _ := newTestEnrollmentHandler(t, "fake-provider", adapter)
	trigger := &fakeDiscoveryTrigger{}
	h.SetDiscoveryTrigger(trigger)
	mux := newTestEnrollmentMux(h)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, connectRequest("fake-provider", canaryKey, ""))
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body = %q", rec.Code, rec.Body.String())
	}

	var body struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(trigger.calledWith) != 1 || trigger.calledWith[0] != body.Data.ID {
		t.Fatalf("discovery trigger calls = %v, want [%s]", trigger.calledWith, body.Data.ID)
	}
}

func TestEnrollment_InvalidKey_TriggersNoDiscovery(t *testing.T) {
	adapter := newFakeAPIKeyAdapter() // no validKey -> invalid
	h, _ := newTestEnrollmentHandler(t, "fake-provider", adapter)
	trigger := &fakeDiscoveryTrigger{}
	h.SetDiscoveryTrigger(trigger)
	mux := newTestEnrollmentMux(h)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, connectRequest("fake-provider", "totally-wrong-key", ""))
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", rec.Code)
	}
	if len(trigger.calledWith) != 0 {
		t.Fatalf("discovery triggered on a failed connect: %v", trigger.calledWith)
	}
}
