package quota

import (
	"errors"
	"testing"
	"time"
)

func TestParseCooldownScope(t *testing.T) {
	for _, s := range []CooldownScope{CooldownScopeAccount, CooldownScopeOffering, CooldownScopeProvider} {
		got, err := ParseCooldownScope(string(s))
		if err != nil || got != s {
			t.Fatalf("ParseCooldownScope(%q) = (%q, %v), want (%q, nil)", s, got, err, s)
		}
	}
	for _, bad := range []string{"", "Account", "accountid", "region"} {
		if _, err := ParseCooldownScope(bad); !errors.Is(err, ErrUnknownCooldownScope) {
			t.Fatalf("ParseCooldownScope(%q) error = %v, want ErrUnknownCooldownScope", bad, err)
		}
	}
}

func TestParseCooldownSource(t *testing.T) {
	for _, s := range []CooldownSource{CooldownSourceRetryAfter, CooldownSourceDefaultBackoff} {
		got, err := ParseCooldownSource(string(s))
		if err != nil || got != s {
			t.Fatalf("ParseCooldownSource(%q) = (%q, %v), want (%q, nil)", s, got, err, s)
		}
	}
	for _, bad := range []string{"", "RetryAfter", "retry-after", "manual"} {
		if _, err := ParseCooldownSource(bad); !errors.Is(err, ErrUnknownCooldownSource) {
			t.Fatalf("ParseCooldownSource(%q) error = %v, want ErrUnknownCooldownSource", bad, err)
		}
	}
}

func TestIsOnCooldown(t *testing.T) {
	base := time.Unix(1000, 0)
	c := Cooldown{Until: base}

	if IsOnCooldown(c, base) {
		t.Fatal("IsOnCooldown at the exact boundary instant = true, want false (strictly before)")
	}
	if IsOnCooldown(c, base.Add(time.Second)) {
		t.Fatal("IsOnCooldown after Until = true, want false")
	}
	if !IsOnCooldown(c, base.Add(-time.Second)) {
		t.Fatal("IsOnCooldown before Until = false, want true")
	}
}
