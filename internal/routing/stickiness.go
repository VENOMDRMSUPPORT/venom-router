package routing

import (
	"container/list"
	"crypto/sha256"
	"encoding/hex"
	"sync"
	"time"
)

// StickinessTTL is the session-stickiness pin lifetime (05 §2 Step 7): a pin
// older than this is treated as absent, so a conversation that goes quiet for
// longer than the TTL is re-distributed normally rather than clinging to a
// possibly-stale account binding.
const StickinessTTL = 15 * time.Minute

// DefaultStickinessCapacity is the LRU's default entry cap (~500 per the
// P4-ROUTE-012 card): large enough for many concurrent conversations, small
// enough that the cache stays a bounded, in-memory preference structure.
const DefaultStickinessCapacity = 500

// stickinessKeyBytes is the number of leading digest bytes retained for a
// stickiness key. 8 bytes = 16 hex characters = a 64-bit space.
const stickinessKeyBytes = 8

// StickinessKey derives the session-stickiness key from the conversation's
// first user message (05 §2 Step 7): sha256 of the message, hex-encoded, first
// 16 hex characters. The raw message is hashed and immediately discarded — this
// function never stores, logs, or returns the content itself, only the digest.
//
// 16 hex chars (8 bytes, 64 bits) is deliberately a preference-cache identifier,
// not a security primitive: a collision merely causes two conversations to
// share one cache slot, which can only ever yield a SUB-OPTIMAL prompt-cache
// pick (a valid, eligible account is still chosen) — never a wrong eligibility
// decision, since every sticky pick is independently re-gated in SelectAccount.
func StickinessKey(firstUserMessage string) string {
	sum := sha256.Sum256([]byte(firstUserMessage))
	return hex.EncodeToString(sum[:stickinessKeyBytes])
}

// stickinessEntry is one LRU node's payload: the pinned account and when it was
// pinned (for TTL evaluation). The stickiness KEY is stored as the list value's
// key field, never any message content.
type stickinessEntry struct {
	key       string
	accountID string
	pinnedAt  time.Time
}

// StickinessCache is a bounded in-memory LRU mapping a stickiness key to a
// pinned AccountID (05 §2 Step 7: "in-memory LRU (~500 entries), fail-open").
// It is the ONE piece of long-lived state this unit owns; nothing here is
// persisted. All time comparisons use an injected `now`, so the type carries no
// wall clock and is fully deterministic under test.
//
// It is safe for concurrent use — the real router shares one cache across many
// in-flight requests — via a single mutex. The lock does not alter any
// single-threaded observable behavior; tests see identical results with or
// without it.
type StickinessCache struct {
	mu       sync.Mutex
	capacity int
	byKey    map[string]*list.Element // key → element holding *stickinessEntry
	order    *list.List               // front = most-recently-used
}

// NewStickinessCache builds an empty cache with the given capacity. A
// non-positive capacity falls back to DefaultStickinessCapacity.
func NewStickinessCache(capacity int) *StickinessCache {
	if capacity < 1 {
		capacity = DefaultStickinessCapacity
	}
	return &StickinessCache{
		capacity: capacity,
		byKey:    make(map[string]*list.Element, capacity),
		order:    list.New(),
	}
}

// Lookup returns the AccountID pinned for key, or ok=false when the key is
// absent or its pin is older than StickinessTTL as of `now`. A successful
// lookup counts as a use and refreshes the entry's LRU recency. An expired
// entry is dropped in passing (lazy eviction) and reported absent.
func (c *StickinessCache) Lookup(key string, now time.Time) (accountID string, ok bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	elem, present := c.byKey[key]
	if !present {
		return "", false
	}
	entry := elem.Value.(*stickinessEntry)
	if now.Sub(entry.pinnedAt) > StickinessTTL {
		// Expired: remove so a stale binding never lingers or blocks LRU space.
		c.removeElement(elem)
		return "", false
	}
	c.order.MoveToFront(elem)
	return entry.accountID, true
}

// Pin records (or refreshes) the binding key → accountID as of `now`, marking it
// most-recently-used. Per the Step-7 contract this is invoked by the CALLER only
// after a genuinely successful response (P4-ROUTE-013 owns detecting "success");
// SelectAccount never calls it. When a new key pushes the cache past capacity,
// the least-recently-used entry is evicted.
func (c *StickinessCache) Pin(key, accountID string, now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if elem, present := c.byKey[key]; present {
		entry := elem.Value.(*stickinessEntry)
		entry.accountID = accountID
		entry.pinnedAt = now
		c.order.MoveToFront(elem)
		return
	}

	elem := c.order.PushFront(&stickinessEntry{key: key, accountID: accountID, pinnedAt: now})
	c.byKey[key] = elem

	for c.order.Len() > c.capacity {
		if back := c.order.Back(); back != nil {
			c.removeElement(back)
		}
	}
}

// removeElement unlinks an element from both the order list and the index. The
// caller must hold c.mu.
func (c *StickinessCache) removeElement(elem *list.Element) {
	entry := elem.Value.(*stickinessEntry)
	c.order.Remove(elem)
	delete(c.byKey, entry.key)
}
