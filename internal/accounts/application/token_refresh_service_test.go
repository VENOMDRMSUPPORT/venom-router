package application_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/accounts/application"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/accounts/domain"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/providers"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/secrets"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/storage"
)

// oauthTokenJSON marshals the stored-token envelope every OAuth adapter
// persists ({"access_token","refresh_token","expires_at"}).
func oauthTokenJSON(t *testing.T, access, refresh string, expiresAt int64) string {
	t.Helper()
	raw, err := json.Marshal(map[string]any{
		"access_token": access, "refresh_token": refresh, "expires_at": expiresAt,
	})
	if err != nil {
		t.Fatalf("marshal token: %v", err)
	}
	return string(raw)
}

// refreshFakeAdapter is a providers.OAuthAdapter whose RefreshCredentials
// is scripted; Begin/Complete are never called by the refresh service.
type refreshFakeAdapter struct {
	refreshed providers.StoredCredentials
	err       error
	calls     int
	lastValue string
}

func (f *refreshFakeAdapter) BeginOAuth(context.Context, string, string, string) (string, error) {
	return "", errors.New("not used")
}
func (f *refreshFakeAdapter) CompleteOAuth(context.Context, string, string, string) (providers.IdentityResult, providers.StoredCredentials, error) {
	return providers.IdentityResult{}, providers.StoredCredentials{}, errors.New("not used")
}
func (f *refreshFakeAdapter) RefreshCredentials(_ context.Context, creds providers.StoredCredentials) (providers.StoredCredentials, error) {
	f.calls++
	f.lastValue = creds.Value
	if f.err != nil {
		return providers.StoredCredentials{}, f.err
	}
	return f.refreshed, nil
}

// seedConnectedOAuthAccount inserts a connected oauth2 account plus an
// active oauth2 credential sealed through the real CredentialService, and
// returns the credential id and the domain account.
func seedConnectedOAuthAccount(t *testing.T, db *storage.DB, kr *secrets.Keyring, now time.Time, providerID, accountID, plaintext string) (string, domain.Account) {
	t.Helper()
	seedProvider(t, db, providerID)
	if _, err := db.Conn().Exec(
		`INSERT INTO accounts (id, provider_id, external_id, auth_type, connection_state, health_state, created_at, updated_at)
		 VALUES (?, ?, ?, 'oauth2', 'connected', 'healthy', 0, 0)`,
		accountID, providerID, accountID,
	); err != nil {
		t.Fatalf("seed connected account: %v", err)
	}

	svc := application.NewCredentialService(storage.NewAccountCredentialRepo(db), kr, func() time.Time { return now })
	credID := accountID + "-cred"
	if _, err := svc.Store(context.Background(), application.StoreCredentialParams{
		ID: credID, AccountID: accountID, ProviderID: providerID,
		Kind: domain.CredentialKindOAuth2, Active: true, PlaintextKey: plaintext,
	}); err != nil {
		t.Fatalf("store credential: %v", err)
	}

	account, ok, err := storage.NewAccountRepo(db).GetByID(context.Background(), accountID)
	if err != nil || !ok {
		t.Fatalf("GetByID: ok=%v err=%v", ok, err)
	}
	return credID, account
}

func readPlaintext(t *testing.T, svc *application.CredentialService, credID string) string {
	t.Helper()
	var got string
	if err := svc.Use(context.Background(), credID, func(pt []byte) error {
		got = string(pt)
		return nil
	}); err != nil {
		t.Fatalf("Use: %v", err)
	}
	return got
}

func TestTokenRefresh_FreshTokenIsLeftAlone(t *testing.T) {
	db := migratedDB(t)
	kr := newTestKeyring(t)
	now := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }

	stored := oauthTokenJSON(t, "at-old", "rt-old", now.Add(time.Hour).Unix()) // expires in 1h > 15m lead
	credID, account := seedConnectedOAuthAccount(t, db, kr, now, "prov-oauth", "acct1", stored)

	adapter := &refreshFakeAdapter{}
	svc := application.NewTokenRefreshService(
		application.NewCredentialService(storage.NewAccountCredentialRepo(db), kr, clock),
		storage.NewAccountRepo(db), 0, clock,
	)

	outcome, err := svc.RefreshIfNeeded(context.Background(), account, credID, adapter)
	if err != nil {
		t.Fatalf("RefreshIfNeeded: %v", err)
	}
	if outcome != application.TokenRefreshFresh {
		t.Fatalf("outcome = %s, want fresh", outcome)
	}
	if adapter.calls != 0 {
		t.Fatalf("adapter called %d times for a fresh token, want 0", adapter.calls)
	}
}

func TestTokenRefresh_NearExpiryRotatesAndPersists(t *testing.T) {
	db := migratedDB(t)
	kr := newTestKeyring(t)
	now := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }

	stored := oauthTokenJSON(t, "at-old", "rt-old", now.Add(5*time.Minute).Unix()) // inside the 15m lead
	credID, account := seedConnectedOAuthAccount(t, db, kr, now, "prov-oauth", "acct1", stored)

	newExpiry := now.Add(time.Hour).Unix()
	rotatedValue := oauthTokenJSON(t, "at-new", "rt-new", newExpiry)
	adapter := &refreshFakeAdapter{refreshed: providers.StoredCredentials{Value: rotatedValue}}

	credSvc := application.NewCredentialService(storage.NewAccountCredentialRepo(db), kr, clock)
	svc := application.NewTokenRefreshService(credSvc, storage.NewAccountRepo(db), 0, clock)

	outcome, err := svc.RefreshIfNeeded(context.Background(), account, credID, adapter)
	if err != nil {
		t.Fatalf("RefreshIfNeeded: %v", err)
	}
	if outcome != application.TokenRefreshRotated {
		t.Fatalf("outcome = %s, want rotated", outcome)
	}
	if adapter.calls != 1 {
		t.Fatalf("adapter calls = %d, want 1", adapter.calls)
	}
	if adapter.lastValue != stored {
		t.Fatalf("adapter received %q, want the stored plaintext", adapter.lastValue)
	}

	// The rotated token must round-trip through the same credential id.
	if got := readPlaintext(t, credSvc, credID); got != rotatedValue {
		t.Fatalf("stored plaintext after rotate = %q, want the refreshed token", got)
	}

	// The expiry column must carry the new token's expiry.
	creds, err := storage.NewAccountCredentialRepo(db).ListForAccount(context.Background(), "acct1")
	if err != nil || len(creds) != 1 {
		t.Fatalf("ListForAccount: %v (n=%d)", err, len(creds))
	}
	if creds[0].ExpiresAt == nil || creds[0].ExpiresAt.Unix() != newExpiry {
		t.Fatalf("ExpiresAt = %v, want unix %d", creds[0].ExpiresAt, newExpiry)
	}
}

func TestTokenRefresh_MissingExpiryStillAttemptsRefresh(t *testing.T) {
	db := migratedDB(t)
	kr := newTestKeyring(t)
	now := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }

	stored := `{"access_token":"at-old","refresh_token":"rt-old"}` // no expires_at
	credID, account := seedConnectedOAuthAccount(t, db, kr, now, "prov-oauth", "acct1", stored)

	adapter := &refreshFakeAdapter{refreshed: providers.StoredCredentials{Value: oauthTokenJSON(t, "at-new", "rt-new", now.Add(time.Hour).Unix())}}
	svc := application.NewTokenRefreshService(
		application.NewCredentialService(storage.NewAccountCredentialRepo(db), kr, clock),
		storage.NewAccountRepo(db), 0, clock,
	)

	outcome, err := svc.RefreshIfNeeded(context.Background(), account, credID, adapter)
	if err != nil {
		t.Fatalf("RefreshIfNeeded: %v", err)
	}
	if outcome != application.TokenRefreshRotated {
		t.Fatalf("outcome = %s, want rotated (unknown expiry must refresh, never assume far-future)", outcome)
	}
}

func TestTokenRefresh_InvalidCredentialMarksAccountExpired(t *testing.T) {
	db := migratedDB(t)
	kr := newTestKeyring(t)
	now := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }

	stored := oauthTokenJSON(t, "at-old", "rt-dead", now.Add(time.Minute).Unix())
	credID, account := seedConnectedOAuthAccount(t, db, kr, now, "prov-oauth", "acct1", stored)

	adapter := &refreshFakeAdapter{err: providers.ErrInvalidCredential}
	credSvc := application.NewCredentialService(storage.NewAccountCredentialRepo(db), kr, clock)
	svc := application.NewTokenRefreshService(credSvc, storage.NewAccountRepo(db), 0, clock)

	outcome, err := svc.RefreshIfNeeded(context.Background(), account, credID, adapter)
	if !errors.Is(err, providers.ErrInvalidCredential) {
		t.Fatalf("error = %v, want ErrInvalidCredential surfaced", err)
	}
	if outcome != application.TokenRefreshReauthRequired {
		t.Fatalf("outcome = %s, want reauth_required", outcome)
	}

	got, ok, err := storage.NewAccountRepo(db).GetByID(context.Background(), "acct1")
	if err != nil || !ok {
		t.Fatalf("GetByID: ok=%v err=%v", ok, err)
	}
	if got.HealthState != domain.HealthExpired {
		t.Fatalf("HealthState = %s, want expired", got.HealthState)
	}
	if got.LastHealthError != application.TokenRefreshReauthMessage {
		t.Fatalf("LastHealthError = %q, want definitive reauthentication guidance", got.LastHealthError)
	}

	// The dead credential itself must be untouched (re-login replaces it).
	if got := readPlaintext(t, credSvc, credID); got != stored {
		t.Fatalf("stored plaintext changed on a failed refresh")
	}
}

func TestTokenRefresh_TransientErrorChangesNothing(t *testing.T) {
	db := migratedDB(t)
	kr := newTestKeyring(t)
	now := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }

	stored := oauthTokenJSON(t, "at-old", "rt-old", now.Add(time.Minute).Unix())
	credID, account := seedConnectedOAuthAccount(t, db, kr, now, "prov-oauth", "acct1", stored)

	adapter := &refreshFakeAdapter{err: providers.ErrProviderUnavailable}
	credSvc := application.NewCredentialService(storage.NewAccountCredentialRepo(db), kr, clock)
	svc := application.NewTokenRefreshService(credSvc, storage.NewAccountRepo(db), 0, clock)

	outcome, err := svc.RefreshIfNeeded(context.Background(), account, credID, adapter)
	if err == nil {
		t.Fatalf("expected the transient error to surface")
	}
	if outcome != application.TokenRefreshTransient {
		t.Fatalf("outcome = %s, want transient", outcome)
	}

	got, ok, err := storage.NewAccountRepo(db).GetByID(context.Background(), "acct1")
	if err != nil || !ok {
		t.Fatalf("GetByID: ok=%v err=%v", ok, err)
	}
	if got.HealthState != domain.HealthHealthy {
		t.Fatalf("HealthState = %s, want healthy unchanged on transient failure", got.HealthState)
	}
	if got := readPlaintext(t, credSvc, credID); got != stored {
		t.Fatalf("stored plaintext changed on a transient failure")
	}
}

func TestTokenRefresh_SuccessfulRecoveryClearsFalseExpiredHealth(t *testing.T) {
	db := migratedDB(t)
	kr := newTestKeyring(t)
	now := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }

	stored := oauthTokenJSON(t, "at-old", "rt-still-valid", now.Add(-time.Minute).Unix())
	credID, account := seedConnectedOAuthAccount(t, db, kr, now, "clinepass", "acct1", stored)
	account.HealthState = domain.HealthExpired
	if _, ok, err := storage.NewAccountRepo(db).UpdateHealthStateWithError(context.Background(), account.ID, account, "provider rejected the credential (401/403)", now); err != nil || !ok {
		t.Fatalf("seed expired health: ok=%v err=%v", ok, err)
	}

	adapter := &refreshFakeAdapter{refreshed: providers.StoredCredentials{Value: oauthTokenJSON(t, "at-new", "rt-new", now.Add(time.Hour).Unix())}}
	svc := application.NewTokenRefreshService(
		application.NewCredentialService(storage.NewAccountCredentialRepo(db), kr, clock),
		storage.NewAccountRepo(db), 0, clock,
	)
	if outcome, err := svc.RefreshIfNeeded(context.Background(), account, credID, adapter); err != nil || outcome != application.TokenRefreshRotated {
		t.Fatalf("RefreshIfNeeded outcome=%s err=%v", outcome, err)
	}

	got, ok, err := storage.NewAccountRepo(db).GetByID(context.Background(), account.ID)
	if err != nil || !ok {
		t.Fatalf("GetByID: ok=%v err=%v", ok, err)
	}
	if got.HealthState != domain.HealthUnknown {
		t.Fatalf("HealthState = %s, want unknown so the next live probe re-evaluates it", got.HealthState)
	}
	if got.LastHealthError != "" {
		t.Fatalf("LastHealthError = %q, want obsolete false-expired error cleared", got.LastHealthError)
	}
}
