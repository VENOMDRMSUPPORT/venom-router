package httpapi

import (
	"bytes"
	"net/http"
	"sync"
)

// idempotencyHeader is the request header a client supplies to make a
// mutating POST replay-safe (09 §1).
const idempotencyHeader = "Idempotency-Key"

// idempotencyMaxEntries bounds the in-memory store so a client
// hammering distinct keys cannot grow it unboundedly for the life of
// the process — the oldest entry is evicted first (simple FIFO, not an
// LRU: idempotency keys are write-once-read-a-few-times, not a cache
// whose recency matters).
const idempotencyMaxEntries = 4096

// idempotencyEntry is one captured response, replayed verbatim on a
// retried request carrying the same (route, key) pair.
type idempotencyEntry struct {
	status int
	header http.Header
	body   []byte
}

// idempotencyStore is a process-lifetime, in-memory, bounded cache of
// (route, Idempotency-Key) -> captured response (09 §1). There is no
// persistent mutation endpoint yet in this codebase to apply it to —
// this unit builds and proves the seam against a test handler; later
// mutating endpoints (CAPI-003+) construct one shared store and call
// Execute. Not persisted: a process restart naturally forgets replays,
// which is acceptable since idempotency here only protects against a
// client's own retry within one run, not across restarts.
type idempotencyStore struct {
	mu      sync.Mutex
	entries map[string]idempotencyEntry
	order   []string
}

// newIdempotencyStore builds an empty, ready-to-use store.
func newIdempotencyStore() *idempotencyStore {
	return &idempotencyStore{entries: make(map[string]idempotencyEntry)}
}

func idempotencyKey(route, key string) string {
	return route + "\x00" + key
}

// Execute runs fn for route via w/r, replaying a prior captured
// response verbatim if r carries an Idempotency-Key this store has
// already seen for route — fn does NOT run again in that case, so
// whatever side effect it would have performed happens at most once
// per (route, key). A request with no Idempotency-Key header always
// runs fn normally (no idempotency applied, no side effect on the
// store).
func (s *idempotencyStore) Execute(w http.ResponseWriter, r *http.Request, route string, fn http.HandlerFunc) {
	key := r.Header.Get(idempotencyHeader)
	if key == "" {
		fn(w, r)
		return
	}

	k := idempotencyKey(route, key)

	s.mu.Lock()
	if entry, ok := s.entries[k]; ok {
		s.mu.Unlock()
		replayResponse(w, entry)
		return
	}
	s.mu.Unlock()

	cw := newCapturingResponseWriter()
	fn(cw, r)
	entry := idempotencyEntry{status: cw.status, header: cw.Header().Clone(), body: cw.body.Bytes()}
	if entry.status == 0 {
		entry.status = http.StatusOK
	}

	s.mu.Lock()
	s.store(k, entry)
	s.mu.Unlock()

	replayResponse(w, entry)
}

// store records entry under k, evicting the oldest entry first if the
// store is already at idempotencyMaxEntries. Callers must hold s.mu.
func (s *idempotencyStore) store(k string, entry idempotencyEntry) {
	if _, exists := s.entries[k]; !exists && len(s.entries) >= idempotencyMaxEntries {
		oldest := s.order[0]
		s.order = s.order[1:]
		delete(s.entries, oldest)
	}
	if _, exists := s.entries[k]; !exists {
		s.order = append(s.order, k)
	}
	s.entries[k] = entry
}

func replayResponse(w http.ResponseWriter, entry idempotencyEntry) {
	dst := w.Header()
	for k, v := range entry.header {
		dst[k] = v
	}
	w.WriteHeader(entry.status)
	_, _ = w.Write(entry.body)
}

// capturingResponseWriter is a minimal in-process http.ResponseWriter
// that records the status/headers/body written to it instead of
// sending them anywhere — the production analogue of
// httptest.ResponseRecorder (which this package cannot import outside
// _test.go files), used so idempotencyStore can run a handler once and
// keep its output to replay later.
type capturingResponseWriter struct {
	header http.Header
	status int
	body   bytes.Buffer
}

func newCapturingResponseWriter() *capturingResponseWriter {
	return &capturingResponseWriter{header: make(http.Header)}
}

func (c *capturingResponseWriter) Header() http.Header { return c.header }

func (c *capturingResponseWriter) Write(b []byte) (int, error) { return c.body.Write(b) }

func (c *capturingResponseWriter) WriteHeader(status int) { c.status = status }
