package quota

import (
	"errors"
	"testing"
	"time"
)

func intPtr(v int) *int             { return &v }
func float64Ptr(v float64) *float64 { return &v }

func TestNormalizeWindowKey_Table(t *testing.T) {
	tests := []struct {
		name    string
		in      WindowKeyInput
		want    string
		wantErr error
	}{
		{
			name: "provider key normalizes to snake_case",
			in:   WindowKeyInput{ProviderKey: "Five Hour", Unit: UnitRequests},
			want: "provider:five_hour",
		},
		{
			name: "provider key with surrounding whitespace and punctuation",
			in:   WindowKeyInput{ProviderKey: " 7-DAY ", Unit: UnitRequests},
			want: "provider:7_day",
		},
		{
			name: "provider key present and duration present: provider wins",
			in:   WindowKeyInput{ProviderKey: "five_hour", DurationSeconds: intPtr(300), Unit: UnitRequests},
			want: "provider:five_hour",
		},
		{
			name: "no provider key, 60s duration",
			in:   WindowKeyInput{DurationSeconds: intPtr(60), Unit: UnitRequests},
			want: "rolling:60s",
		},
		{
			name: "no provider key, 3600s duration",
			in:   WindowKeyInput{DurationSeconds: intPtr(3600), Unit: UnitRequests},
			want: "rolling:3600s",
		},
		{
			name: "no provider key, nil duration, concurrency unit",
			in:   WindowKeyInput{Unit: UnitConcurrency},
			want: "local:concurrency",
		},
		{
			name: "provider key of only punctuation falls through, never 'provider:'",
			in:   WindowKeyInput{ProviderKey: "!!!", Unit: UnitConcurrency},
			want: "local:concurrency",
		},
		{
			name: "zero duration treated as absent",
			in:   WindowKeyInput{DurationSeconds: intPtr(0), Unit: UnitRequests},
			want: "local:requests",
		},
		{
			name: "negative duration treated as absent",
			in:   WindowKeyInput{DurationSeconds: intPtr(-5), Unit: UnitRequests},
			want: "local:requests",
		},
		{
			name:    "invalid unit is rejected",
			in:      WindowKeyInput{Unit: Unit("bogus")},
			wantErr: ErrUnknownUnit,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := NormalizeWindowKey(tc.in)
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("NormalizeWindowKey(%+v) error = %v, want %v", tc.in, err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("NormalizeWindowKey(%+v) unexpected error: %v", tc.in, err)
			}
			if got != tc.want {
				t.Fatalf("NormalizeWindowKey(%+v) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestNormalizeWindowKey_Deterministic(t *testing.T) {
	in := WindowKeyInput{ProviderKey: "Five Hour", Unit: UnitRequests}

	first, err := NormalizeWindowKey(in)
	if err != nil {
		t.Fatalf("NormalizeWindowKey: %v", err)
	}
	if first == "" {
		t.Fatalf("NormalizeWindowKey(%+v) = \"\", want non-empty", in)
	}

	for i := 0; i < 100; i++ {
		got, err := NormalizeWindowKey(in)
		if err != nil {
			t.Fatalf("NormalizeWindowKey iteration %d: %v", i, err)
		}
		if got != first {
			t.Fatalf("NormalizeWindowKey iteration %d = %q, want %q (must be deterministic)", i, got, first)
		}
	}

	// A second, independently constructed but identical input must
	// produce the same key.
	second := WindowKeyInput{ProviderKey: "Five Hour", Unit: UnitRequests}
	gotSecond, err := NormalizeWindowKey(second)
	if err != nil {
		t.Fatalf("NormalizeWindowKey(second): %v", err)
	}
	if gotSecond != first {
		t.Fatalf("NormalizeWindowKey(second identical input) = %q, want %q", gotSecond, first)
	}

	// A window with no provider key and no duration must never
	// normalize to "".
	local, err := NormalizeWindowKey(WindowKeyInput{Unit: UnitConcurrency})
	if err != nil {
		t.Fatalf("NormalizeWindowKey(local): %v", err)
	}
	if local == "" {
		t.Fatalf("NormalizeWindowKey(local-safety input) = \"\", want non-empty")
	}
}

func TestParseVocabularies_FailClosed(t *testing.T) {
	t.Run("Source", func(t *testing.T) {
		for _, s := range []Source{SourceProviderEvidence, SourceLocalSafety, SourceOwnerOverride} {
			got, err := ParseSource(string(s))
			if err != nil {
				t.Fatalf("ParseSource(%q): %v, want success", s, err)
			}
			if got != s {
				t.Fatalf("ParseSource(%q) = %q, want %q", s, got, s)
			}
		}

		// provider_policy / owner_policy are real
		// account_funding_evidence.source vocabulary tokens (02 §2) —
		// deliberately rejected here to prove the two vocabularies are
		// never conflated.
		for _, bad := range []string{"provider_policy", "owner_policy", "", "Provider_Evidence", "unknown"} {
			got, err := ParseSource(bad)
			if !errors.Is(err, ErrUnknownSource) {
				t.Fatalf("ParseSource(%q) error = %v, want ErrUnknownSource", bad, err)
			}
			if got != "" {
				t.Fatalf("ParseSource(%q) = %q, want zero value", bad, got)
			}
		}
	})

	t.Run("Unit", func(t *testing.T) {
		legal := []Unit{UnitRequests, UnitInputTokens, UnitOutputTokens, UnitTokens, UnitCredits, UnitBalance, UnitPercent, UnitConcurrency}
		for _, u := range legal {
			got, err := ParseUnit(string(u))
			if err != nil {
				t.Fatalf("ParseUnit(%q): %v, want success", u, err)
			}
			if got != u {
				t.Fatalf("ParseUnit(%q) = %q, want %q", u, got, u)
			}
		}
		for _, bad := range []string{"Requests", "", "byte"} {
			got, err := ParseUnit(bad)
			if !errors.Is(err, ErrUnknownUnit) {
				t.Fatalf("ParseUnit(%q) error = %v, want ErrUnknownUnit", bad, err)
			}
			if got != "" {
				t.Fatalf("ParseUnit(%q) = %q, want zero value", bad, got)
			}
		}
	})

	t.Run("Freshness", func(t *testing.T) {
		legal := []Freshness{FreshnessFresh, FreshnessStale, FreshnessUnknown}
		for _, f := range legal {
			got, err := ParseFreshness(string(f))
			if err != nil {
				t.Fatalf("ParseFreshness(%q): %v, want success", f, err)
			}
			if got != f {
				t.Fatalf("ParseFreshness(%q) = %q, want %q", f, got, f)
			}
		}
		for _, bad := range []string{"Fresh", "", "expired"} {
			got, err := ParseFreshness(bad)
			if !errors.Is(err, ErrUnknownFreshness) {
				t.Fatalf("ParseFreshness(%q) error = %v, want ErrUnknownFreshness", bad, err)
			}
			if got != "" {
				t.Fatalf("ParseFreshness(%q) = %q, want zero value", bad, got)
			}
		}
	})
}

func TestWindow_Capacity(t *testing.T) {
	tests := []struct {
		name      string
		remaining *float64
		limit     *float64
		wantVal   float64
		wantOK    bool
	}{
		{"remaining set wins", float64Ptr(42), float64Ptr(100), 42, true},
		{"remaining nil, limit_value set (local-safety case)", nil, float64Ptr(7), 7, true},
		{"both nil: unknown", nil, nil, 0, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			w := Window{Remaining: tc.remaining, LimitValue: tc.limit}
			got, ok := w.Capacity()
			if ok != tc.wantOK {
				t.Fatalf("Capacity() ok = %v, want %v", ok, tc.wantOK)
			}
			if ok && got != tc.wantVal {
				t.Fatalf("Capacity() = %v, want %v", got, tc.wantVal)
			}
		})
	}
}

func TestWindow_State_Table(t *testing.T) {
	now := time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name string
		w    Window
		need float64
		want WindowState
	}{
		{
			name: "unknown capacity",
			w:    Window{Freshness: FreshnessFresh, ObservedAt: now, Remaining: nil, LimitValue: nil},
			need: 1,
			want: StateUnknown,
		},
		{
			name: "freshness unknown",
			w:    Window{Freshness: FreshnessUnknown, ObservedAt: now, Remaining: float64Ptr(100)},
			need: 1,
			want: StateUnknown,
		},
		{
			name: "observed 16 min ago is stale, not exhausted, even with zero headroom",
			w: Window{
				Freshness:  FreshnessFresh,
				ObservedAt: now.Add(-16 * time.Minute),
				Remaining:  float64Ptr(0),
				Reserved:   0,
			},
			need: 1,
			want: StateStale,
		},
		{
			name: "persisted freshness stale",
			w:    Window{Freshness: FreshnessStale, ObservedAt: now, Remaining: float64Ptr(100)},
			need: 1,
			want: StateStale,
		},
		{
			name: "headroom zero is exhausted",
			w:    Window{Freshness: FreshnessFresh, ObservedAt: now, Remaining: float64Ptr(5), Reserved: 5},
			need: 1,
			want: StateExhausted,
		},
		{
			name: "headroom 3 need 5 is insufficient",
			w:    Window{Freshness: FreshnessFresh, ObservedAt: now, Remaining: float64Ptr(3)},
			need: 5,
			want: StateInsufficient,
		},
		{
			name: "headroom 10 need 5 is available",
			w:    Window{Freshness: FreshnessFresh, ObservedAt: now, Remaining: float64Ptr(10)},
			need: 5,
			want: StateAvailable,
		},
		{
			name: "local-safety window bounded by limit_value",
			w:    Window{Freshness: FreshnessFresh, ObservedAt: now, Remaining: nil, LimitValue: float64Ptr(5), Reserved: 5},
			need: 1,
			want: StateExhausted,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.w.State(tc.need, now, DefaultStalenessWindow)
			if got != tc.want {
				t.Fatalf("State() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestMostRestrictive_Table(t *testing.T) {
	tests := []struct {
		name   string
		states []WindowState
		want   WindowState
	}{
		{"all available", []WindowState{StateAvailable, StateAvailable}, StateAvailable},
		{"available and exhausted", []WindowState{StateAvailable, StateExhausted}, StateExhausted},
		{"available and stale: stale is more restrictive", []WindowState{StateAvailable, StateStale}, StateStale},
		{"insufficient beats available", []WindowState{StateAvailable, StateInsufficient}, StateInsufficient},
		{"exhausted beats insufficient", []WindowState{StateInsufficient, StateExhausted}, StateExhausted},
		{"stale then unknown resolves to unknown", []WindowState{StateStale, StateUnknown}, StateUnknown},
		{"unknown then stale resolves to unknown (order independent)", []WindowState{StateUnknown, StateStale}, StateUnknown},
		{"empty set fails closed to unknown", []WindowState{}, StateUnknown},
		{"nil set fails closed to unknown", nil, StateUnknown},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := MostRestrictive(tc.states)
			if got != tc.want {
				t.Fatalf("MostRestrictive(%v) = %q, want %q", tc.states, got, tc.want)
			}
		})
	}
}
