package secrets

import "testing"

func TestValidateOwnerPassword_RejectsTooShort(t *testing.T) {
	if err := ValidateOwnerPassword("short1"); err != ErrPasswordTooShort {
		t.Fatalf("ValidateOwnerPassword(short) error = %v, want ErrPasswordTooShort", err)
	}
}

func TestValidateOwnerPassword_AcceptsLongEnough(t *testing.T) {
	if err := ValidateOwnerPassword("a-long-enough-password"); err != nil {
		t.Fatalf("ValidateOwnerPassword(long) error = %v, want nil", err)
	}
}

func TestDeriveOwnerPasswordHash_RejectsTooShortWithoutDeriving(t *testing.T) {
	_, err := DeriveOwnerPasswordHash("tooshort")
	if err != ErrPasswordTooShort {
		t.Fatalf("DeriveOwnerPasswordHash(tooshort) error = %v, want ErrPasswordTooShort", err)
	}
}

func TestDeriveOwnerPasswordHash_UsesDocumentedParams(t *testing.T) {
	got, err := DeriveOwnerPasswordHash("correct-horse-battery-staple")
	if err != nil {
		t.Fatalf("DeriveOwnerPasswordHash: unexpected error: %v", err)
	}
	if got.Time != Argon2Time || got.MemKiB != Argon2MemKiB || got.Threads != Argon2Threads || got.KeyLen != Argon2KeyLen {
		t.Fatalf("KDF params = %+v, want time=%d mem_kib=%d threads=%d key_len=%d", got, Argon2Time, Argon2MemKiB, Argon2Threads, Argon2KeyLen)
	}
	if len(got.Salt) != saltLen {
		t.Fatalf("len(Salt) = %d, want %d", len(got.Salt), saltLen)
	}
	if len(got.Hash) != int(Argon2KeyLen) {
		t.Fatalf("len(Hash) = %d, want %d", len(got.Hash), Argon2KeyLen)
	}
}

func TestDeriveOwnerPasswordHash_SaltIsFreshEachCall(t *testing.T) {
	a, err := DeriveOwnerPasswordHash("correct-horse-battery-staple")
	if err != nil {
		t.Fatalf("DeriveOwnerPasswordHash: unexpected error: %v", err)
	}
	b, err := DeriveOwnerPasswordHash("correct-horse-battery-staple")
	if err != nil {
		t.Fatalf("DeriveOwnerPasswordHash: unexpected error: %v", err)
	}

	if string(a.Salt) == string(b.Salt) {
		t.Fatalf("two derivations of the same password produced identical salts, want fresh random salt per call")
	}
	if string(a.Hash) == string(b.Hash) {
		t.Fatalf("two derivations of the same password produced identical hashes, want distinct hashes (different salts)")
	}
}

func TestVerifyOwnerPassword_CorrectPasswordVerifies(t *testing.T) {
	stored, err := DeriveOwnerPasswordHash("correct-horse-battery-staple")
	if err != nil {
		t.Fatalf("DeriveOwnerPasswordHash: unexpected error: %v", err)
	}

	if !VerifyOwnerPassword("correct-horse-battery-staple", stored) {
		t.Fatalf("VerifyOwnerPassword(correct password) = false, want true")
	}
}

func TestVerifyOwnerPassword_WrongPasswordRejected(t *testing.T) {
	stored, err := DeriveOwnerPasswordHash("correct-horse-battery-staple")
	if err != nil {
		t.Fatalf("DeriveOwnerPasswordHash: unexpected error: %v", err)
	}

	if VerifyOwnerPassword("wrong-horse-battery-staple", stored) {
		t.Fatalf("VerifyOwnerPassword(wrong password) = true, want false")
	}
}
