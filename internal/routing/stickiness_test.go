package routing

import (
	"regexp"
	"testing"
	"time"
)

var hex16 = regexp.MustCompile(`^[0-9a-f]{16}$`)

// TestStickinessKey_Deterministic proves the key is a stable, 16-lowercase-hex
// digest of the input: identical input → identical key, and the output shape is
// always exactly 16 hex characters.
func TestStickinessKey_Deterministic(t *testing.T) {
	const msg = "hello, how do I reset my password?"
	k1 := StickinessKey(msg)
	k2 := StickinessKey(msg)
	if k1 != k2 {
		t.Fatalf("determinism: %q != %q for identical input", k1, k2)
	}
	if !hex16.MatchString(k1) {
		t.Fatalf("shape: key %q is not 16 lowercase hex chars", k1)
	}
	// The empty string still hashes to a valid 16-hex key.
	if e := StickinessKey(""); !hex16.MatchString(e) {
		t.Fatalf("shape: empty-input key %q is not 16 lowercase hex chars", e)
	}
}

// TestStickinessKey_DifferentInputsDiffer proves distinct inputs (overwhelmingly
// likely) map to distinct keys — a minimal guard that the digest actually
// depends on the whole input, not a constant.
func TestStickinessKey_DifferentInputsDiffer(t *testing.T) {
	if StickinessKey("first message A") == StickinessKey("first message B") {
		t.Fatalf("two different inputs collided in the 16-hex space (unexpected)")
	}
}

// TestStickinessCache_PinThenLookup proves a fresh pin is found by a lookup
// within the TTL and returns the pinned AccountID.
func TestStickinessCache_PinThenLookup(t *testing.T) {
	c := NewStickinessCache(4)
	now := drrTestNow
	c.Pin("k1", "acc-1", now)

	got, ok := c.Lookup("k1", now.Add(1*time.Minute))
	if !ok {
		t.Fatalf("fresh pin not found within TTL")
	}
	if got != "acc-1" {
		t.Fatalf("lookup returned %q, want acc-1", got)
	}
	// A key never pinned is absent.
	if _, ok := c.Lookup("missing", now); ok {
		t.Fatalf("absent key reported present")
	}
}

// TestStickinessCache_TTLExpiry proves a lookup past the 15-minute TTL returns
// ok=false.
//
// Mutation row S-M1: remove the TTL check in Lookup → the expired pin is
// reported present → this test RED.
func TestStickinessCache_TTLExpiry(t *testing.T) {
	c := NewStickinessCache(4)
	now := drrTestNow
	c.Pin("k1", "acc-1", now)

	// Just inside TTL: still present.
	if _, ok := c.Lookup("k1", now.Add(StickinessTTL)); !ok {
		t.Fatalf("pin at exactly TTL age should still be valid")
	}
	// Past TTL: gone.
	if _, ok := c.Lookup("k1", now.Add(StickinessTTL+time.Second)); ok {
		t.Fatalf("pin older than TTL must return ok=false")
	}
}

// TestStickinessCache_LRUEviction proves that exceeding capacity evicts the
// least-recently-used entry.
func TestStickinessCache_LRUEviction(t *testing.T) {
	c := NewStickinessCache(2)
	now := drrTestNow
	c.Pin("a", "acc-a", now)
	c.Pin("b", "acc-b", now)
	// Third distinct key exceeds capacity → LRU ("a") evicted.
	c.Pin("c", "acc-c", now)

	if _, ok := c.Lookup("a", now); ok {
		t.Fatalf("LRU eviction: 'a' should have been evicted")
	}
	if _, ok := c.Lookup("b", now); !ok {
		t.Fatalf("LRU eviction: 'b' should remain")
	}
	if _, ok := c.Lookup("c", now); !ok {
		t.Fatalf("LRU eviction: 'c' should remain")
	}
}

// TestStickinessCache_LookupRefreshesRecency proves a successful lookup counts
// as "use", protecting that entry from being the next eviction victim.
func TestStickinessCache_LookupRefreshesRecency(t *testing.T) {
	c := NewStickinessCache(2)
	now := drrTestNow
	c.Pin("a", "acc-a", now)
	c.Pin("b", "acc-b", now)

	// Touch "a" so "b" becomes least-recently-used.
	if _, ok := c.Lookup("a", now.Add(time.Minute)); !ok {
		t.Fatalf("precondition: 'a' should be present")
	}
	// Pin a third key → "b" (now LRU) evicted, "a" preserved.
	c.Pin("c", "acc-c", now.Add(2*time.Minute))

	if _, ok := c.Lookup("a", now.Add(3*time.Minute)); !ok {
		t.Fatalf("recency: 'a' was touched and must survive eviction")
	}
	if _, ok := c.Lookup("b", now.Add(3*time.Minute)); ok {
		t.Fatalf("recency: 'b' was least-recently-used and should be evicted")
	}
}
