package application_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/accounts/application"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/accounts/domain"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/providers"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/storage"
)

// fixtureOpenCodeZenServer mirrors internal/providers' own fixture (a
// separate, minimal copy: this package tests the CONNECT FLOW, not the
// adapter's own parsing — see internal/providers/opencode_zen_test.go
// for that). Accepts exactly "good-key" on the chat probe.
func fixtureOpenCodeZenServer(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "Bearer good-key" {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusUnauthorized)
	})
	mux.HandleFunc("/v1/models", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"data":[]}`))
	})
	return httptest.NewServer(mux)
}

func fixtureChatProbe(serverURL string) providers.ChatProbe {
	return func(ctx context.Context, baseURL, key string) (int, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, serverURL+"/v1/chat/completions", strings.NewReader(`{"max_tokens":1}`))
		if err != nil {
			return 0, err
		}
		req.Header.Set("Authorization", "Bearer "+key)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return 0, err
		}
		defer func() { _ = resp.Body.Close() }()
		return resp.StatusCode, nil
	}
}

// fixtureUnusedModelsDevProbe is the ModelsDevProbe for connect-only
// tests: these flows never run discovery, so it fails LOUDLY if a code
// change starts to.
func fixtureUnusedModelsDevProbe(t *testing.T) providers.ModelsDevProbe {
	t.Helper()
	return func(context.Context) ([]byte, error) {
		t.Fatalf("models.dev probe called in a connect-only test")
		return nil, nil
	}
}

func fixtureModelsProbe(serverURL string) providers.ModelsProbe {
	return func(ctx context.Context, baseURL, key string) ([]byte, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, serverURL+"/v1/models", nil)
		if err != nil {
			return nil, err
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return nil, err
		}
		defer func() { _ = resp.Body.Close() }()
		return []byte(`{"data":[]}`), nil
	}
}

func sequentialIDGenerator(prefix string) application.IDGenerator {
	n := 0
	return func() string {
		n++
		return prefix + "-" + strconv.Itoa(n)
	}
}

func TestConnectService_ValidKey_CreatesExactlyOneAccountCredentialFunding(t *testing.T) {
	server := fixtureOpenCodeZenServer(t)
	defer server.Close()

	db := migratedDB(t)
	seedProvider(t, db, "opencode-zen")

	adapter := providers.NewOpenCodeZenAdapter(fixtureChatProbe(server.URL), fixtureModelsProbe(server.URL), fixtureUnusedModelsDevProbe(t), nil)
	enrollment := storage.NewEnrollmentRepo(db)
	accounts := storage.NewAccountRepo(db)
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	svc := application.NewConnectService(enrollment, accounts, newTestKeyring(t), sequentialIDGenerator("id"), func() time.Time { return now })

	account, err := svc.ConnectAPIKeyAccount(context.Background(), application.ConnectAPIKeyAccountParams{
		ProviderID: "opencode-zen", Adapter: adapter, PlaintextKey: "good-key",
		FundingMode: domain.FundingModeOwnerPolicy,
	})
	if err != nil {
		t.Fatalf("ConnectAPIKeyAccount: %v", err)
	}
	if account.ConnectionState != domain.ConnectionConnected {
		t.Fatalf("ConnectionState = %q, want connected", account.ConnectionState)
	}

	assertCount(t, db, "accounts", 1)
	assertCount(t, db, "account_credentials", 1)
	assertCount(t, db, "account_funding_evidence", 1)

	fund, ok, err := storage.NewFundingEvidenceRepo(db).CurrentForAccount(context.Background(), account.ID)
	if err != nil || !ok {
		t.Fatalf("CurrentForAccount: ok=%v err=%v", ok, err)
	}
	if fund.Funding != domain.FundingFree || fund.Source != domain.FundingSourceOwnerPolicy {
		t.Fatalf("funding = %+v, want free/owner_policy", fund)
	}
}

func TestConnectService_ValidKey_MarksAccountHealthyWithCheckTimestamp(t *testing.T) {
	server := fixtureOpenCodeZenServer(t)
	defer server.Close()

	db := migratedDB(t)
	seedProvider(t, db, "opencode-zen")

	adapter := providers.NewOpenCodeZenAdapter(fixtureChatProbe(server.URL), fixtureModelsProbe(server.URL), fixtureUnusedModelsDevProbe(t), nil)
	enrollment := storage.NewEnrollmentRepo(db)
	accounts := storage.NewAccountRepo(db)
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	svc := application.NewConnectService(enrollment, accounts, newTestKeyring(t), sequentialIDGenerator("id"), func() time.Time { return now })

	account, err := svc.ConnectAPIKeyAccount(context.Background(), application.ConnectAPIKeyAccountParams{
		ProviderID: "opencode-zen", Adapter: adapter, PlaintextKey: "good-key",
		FundingMode: domain.FundingModeOwnerPolicy,
	})
	if err != nil {
		t.Fatalf("ConnectAPIKeyAccount: %v", err)
	}

	// The adapter authenticated the key (03 §1 authentic-validation rule) to
	// reach this point, so the account is HEALTHY on connect, with the check
	// timestamp stamped — not left HealthUnknown / "Checked: —".
	if account.HealthState != domain.HealthHealthy {
		t.Fatalf("returned HealthState = %q, want healthy", account.HealthState)
	}
	if account.LastHealthCheckAt == nil || !account.LastHealthCheckAt.Equal(now) {
		t.Fatalf("returned LastHealthCheckAt = %v, want %v", account.LastHealthCheckAt, now)
	}

	// And the durable row reflects it (not just the returned struct).
	persisted, ok, err := accounts.GetByID(context.Background(), account.ID)
	if err != nil || !ok {
		t.Fatalf("GetByID: ok=%v err=%v", ok, err)
	}
	if persisted.HealthState != domain.HealthHealthy {
		t.Fatalf("persisted HealthState = %q, want healthy", persisted.HealthState)
	}
	if persisted.LastHealthCheckAt == nil || !persisted.LastHealthCheckAt.Equal(now) {
		t.Fatalf("persisted LastHealthCheckAt = %v, want %v", persisted.LastHealthCheckAt, now)
	}
}

func TestConnectService_OwnerOverride_StampsOwnerOverrideSource(t *testing.T) {
	server := fixtureOpenCodeZenServer(t)
	defer server.Close()

	db := migratedDB(t)
	seedProvider(t, db, "opencode-zen")

	adapter := providers.NewOpenCodeZenAdapter(fixtureChatProbe(server.URL), fixtureModelsProbe(server.URL), fixtureUnusedModelsDevProbe(t), nil)
	enrollment := storage.NewEnrollmentRepo(db)
	accounts := storage.NewAccountRepo(db)
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	svc := application.NewConnectService(enrollment, accounts, newTestKeyring(t), sequentialIDGenerator("id"), func() time.Time { return now })

	paid := domain.FundingPaid
	account, err := svc.ConnectAPIKeyAccount(context.Background(), application.ConnectAPIKeyAccountParams{
		ProviderID: "opencode-zen", Adapter: adapter, PlaintextKey: "good-key",
		FundingMode: domain.FundingModeOwnerPolicy, OwnerFunding: &paid,
	})
	if err != nil {
		t.Fatalf("ConnectAPIKeyAccount: %v", err)
	}

	fund, ok, err := storage.NewFundingEvidenceRepo(db).CurrentForAccount(context.Background(), account.ID)
	if err != nil || !ok {
		t.Fatalf("CurrentForAccount: ok=%v err=%v", ok, err)
	}
	if fund.Funding != domain.FundingPaid || fund.Source != domain.FundingSourceOwnerOverride {
		t.Fatalf("funding = %+v, want paid/owner_override", fund)
	}
}

func TestConnectService_DuplicateIdentity_ReturnsAccountAlreadyConnected(t *testing.T) {
	server := fixtureOpenCodeZenServer(t)
	defer server.Close()

	db := migratedDB(t)
	seedProvider(t, db, "opencode-zen")

	adapter := providers.NewOpenCodeZenAdapter(fixtureChatProbe(server.URL), fixtureModelsProbe(server.URL), fixtureUnusedModelsDevProbe(t), nil)
	enrollment := storage.NewEnrollmentRepo(db)
	accounts := storage.NewAccountRepo(db)
	svc := application.NewConnectService(enrollment, accounts, newTestKeyring(t), sequentialIDGenerator("id"), nil)

	params := application.ConnectAPIKeyAccountParams{
		ProviderID: "opencode-zen", Adapter: adapter, PlaintextKey: "good-key",
		FundingMode: domain.FundingModeOwnerPolicy,
	}

	if _, err := svc.ConnectAPIKeyAccount(context.Background(), params); err != nil {
		t.Fatalf("first ConnectAPIKeyAccount: %v", err)
	}
	assertCount(t, db, "accounts", 1)

	_, err := svc.ConnectAPIKeyAccount(context.Background(), params)
	if !errors.Is(err, application.ErrConnectAccountAlreadyConnected) {
		t.Fatalf("second ConnectAPIKeyAccount error = %v, want ErrConnectAccountAlreadyConnected", err)
	}

	// The duplicate attempt must not have created a second account, nor
	// any credential/funding row for it.
	assertCount(t, db, "accounts", 1)
	assertCount(t, db, "account_credentials", 1)
	assertCount(t, db, "account_funding_evidence", 1)
}

func TestConnectService_InvalidKey_CreatesZeroRows(t *testing.T) {
	server := fixtureOpenCodeZenServer(t)
	defer server.Close()

	db := migratedDB(t)
	seedProvider(t, db, "opencode-zen")

	adapter := providers.NewOpenCodeZenAdapter(fixtureChatProbe(server.URL), fixtureModelsProbe(server.URL), fixtureUnusedModelsDevProbe(t), nil)
	enrollment := storage.NewEnrollmentRepo(db)
	accounts := storage.NewAccountRepo(db)
	svc := application.NewConnectService(enrollment, accounts, newTestKeyring(t), sequentialIDGenerator("id"), nil)

	_, err := svc.ConnectAPIKeyAccount(context.Background(), application.ConnectAPIKeyAccountParams{
		ProviderID: "opencode-zen", Adapter: adapter, PlaintextKey: "bad-key",
		FundingMode: domain.FundingModeOwnerPolicy,
	})
	if !errors.Is(err, application.ErrConnectInvalidCredential) {
		t.Fatalf("error = %v, want ErrConnectInvalidCredential", err)
	}

	assertCount(t, db, "accounts", 0)
	assertCount(t, db, "account_credentials", 0)
	assertCount(t, db, "account_funding_evidence", 0)
}

func TestConnectService_UnavailableProvider_CreatesZeroRows(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()

	db := migratedDB(t)
	seedProvider(t, db, "opencode-zen")

	adapter := providers.NewOpenCodeZenAdapter(fixtureChatProbe(server.URL), fixtureModelsProbe(server.URL), fixtureUnusedModelsDevProbe(t), nil)
	enrollment := storage.NewEnrollmentRepo(db)
	accounts := storage.NewAccountRepo(db)
	svc := application.NewConnectService(enrollment, accounts, newTestKeyring(t), sequentialIDGenerator("id"), nil)

	_, err := svc.ConnectAPIKeyAccount(context.Background(), application.ConnectAPIKeyAccountParams{
		ProviderID: "opencode-zen", Adapter: adapter, PlaintextKey: "any-key",
		FundingMode: domain.FundingModeOwnerPolicy,
	})
	if !errors.Is(err, application.ErrConnectProviderUnavailable) {
		t.Fatalf("error = %v, want ErrConnectProviderUnavailable", err)
	}

	assertCount(t, db, "accounts", 0)
	assertCount(t, db, "account_credentials", 0)
	assertCount(t, db, "account_funding_evidence", 0)
}

func assertCount(t *testing.T, db *storage.DB, table string, want int) {
	t.Helper()
	var got int
	if err := db.Conn().QueryRow("SELECT COUNT(*) FROM " + table).Scan(&got); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	if got != want {
		t.Fatalf("row count in %s = %d, want %d", table, got, want)
	}
}
