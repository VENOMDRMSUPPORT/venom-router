package secrets

import "testing"

func TestMintOwnerSession_HandleAndHashAreConsistent(t *testing.T) {
	session, err := MintOwnerSession()
	if err != nil {
		t.Fatalf("MintOwnerSession: unexpected error: %v", err)
	}
	if session.Handle == "" {
		t.Fatalf("Handle is empty, want a high-entropy opaque value")
	}
	if len(session.TokenHash) != 32 {
		t.Fatalf("len(TokenHash) = %d, want 32 (SHA-256)", len(session.TokenHash))
	}

	want := HashSessionHandle(session.Handle)
	if string(session.TokenHash) != string(want) {
		t.Fatalf("TokenHash does not match HashSessionHandle(Handle) — verifier must be derived from the returned handle")
	}
}

func TestMintOwnerSession_HandleIsFreshEachCall(t *testing.T) {
	a, err := MintOwnerSession()
	if err != nil {
		t.Fatalf("MintOwnerSession: unexpected error: %v", err)
	}
	b, err := MintOwnerSession()
	if err != nil {
		t.Fatalf("MintOwnerSession: unexpected error: %v", err)
	}

	if a.Handle == b.Handle {
		t.Fatalf("two MintOwnerSession calls produced identical handles, want fresh randomness per call")
	}
	if string(a.TokenHash) == string(b.TokenHash) {
		t.Fatalf("two MintOwnerSession calls produced identical token hashes, want distinct hashes")
	}
}

func TestHashSessionHandle_Deterministic(t *testing.T) {
	a := HashSessionHandle("some-handle-value")
	b := HashSessionHandle("some-handle-value")
	if string(a) != string(b) {
		t.Fatalf("HashSessionHandle is not deterministic for the same input")
	}

	c := HashSessionHandle("a-different-handle-value")
	if string(a) == string(c) {
		t.Fatalf("HashSessionHandle produced the same hash for different inputs")
	}
}
