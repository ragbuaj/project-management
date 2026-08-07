package domain_test

import (
	"errors"
	"strings"
	"testing"

	identitydom "github.com/ragbuaj/project-management/backend/internal/modules/identity/domain"
)

func TestAHashedPasswordVerifiesAndAWrongOneDoesNot(t *testing.T) {
	t.Parallel()

	const password = "correct horse battery staple"

	encoded, err := identitydom.HashPassword(password)
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}

	if err := identitydom.VerifyPassword(encoded, password); err != nil {
		t.Errorf("VerifyPassword with the right password: %v", err)
	}

	wrong := []struct {
		name  string
		guess string
	}{
		{"a different password", "correct horse battery stapl3"},
		{"the same password one byte short", password[:len(password)-1]},
		{"an empty password", ""},
		{"the encoded hash itself", encoded},
	}

	for _, c := range wrong {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			if err := identitydom.VerifyPassword(encoded, c.guess); !errors.Is(err, identitydom.ErrPasswordMismatch) {
				t.Errorf("VerifyPassword(%q) = %v, want ErrPasswordMismatch", c.guess, err)
			}
		})
	}
}

// Two accounts with the same password must not share a hash, or the rows
// themselves tell an attacker which accounts to try a single guess against.
func TestTheSamePasswordHashesDifferentlyEveryTime(t *testing.T) {
	t.Parallel()

	const password = "the same password twice"

	first, err := identitydom.HashPassword(password)
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}

	second, err := identitydom.HashPassword(password)
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}

	if first == second {
		t.Fatal("hashing the same password twice produced the same string, so the salt is not random")
	}

	for _, encoded := range []string{first, second} {
		if err := identitydom.VerifyPassword(encoded, password); err != nil {
			t.Errorf("VerifyPassword: %v", err)
		}
	}
}

// The stored string carries its own parameters, which is what makes raising
// the cost later possible without invalidating every account.
func TestTheStoredHashCarriesTheParametersItWasMadeWith(t *testing.T) {
	t.Parallel()

	encoded, err := identitydom.HashPassword("whatever passes the policy")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}

	const want = "$argon2id$v=19$m=19456,t=2,p=1$"
	if !strings.HasPrefix(encoded, want) {
		t.Errorf("HashPassword produced %q, want it to start with %q", encoded, want)
	}

	if identitydom.NeedsRehash(encoded) {
		t.Error("a hash just written with the current parameters says it needs rehashing")
	}
}

// Everything here is a row this package did not write. None of it may be
// reported as a wrong password: one is a failed login attempt, the other is a
// corrupted table.
func TestAnUnreadableHashIsNotAWrongPassword(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		stored string
	}{
		{"an empty column", ""},
		{"a bcrypt hash", "$2y$12$K8p3nZ0kZ1wq7nS0mF1oXeR8yV1cQqf8W0h0Ff5Wl4kQmVh2t6mBu"},
		{"too few fields", "$argon2id$v=19$m=19456,t=2,p=1$c2FsdA"},
		{"another argon2 variant", "$argon2i$v=19$m=19456,t=2,p=1$c2FsdHNhbHRzYWx0c2FsdA$aGFzaGhhc2hoYXNoaGFzaGhhc2hoYXNoaGFzaGg"},
		{"an unknown version", "$argon2id$v=13$m=19456,t=2,p=1$c2FsdHNhbHRzYWx0c2FsdA$aGFzaGhhc2hoYXNoaGFzaGhhc2hoYXNoaGFzaGg"},
		{"unparsable parameters", "$argon2id$v=19$m=lots,t=2,p=1$c2FsdHNhbHRzYWx0c2FsdA$aGFzaGhhc2hoYXNoaGFzaGhhc2hoYXNoaGFzaGg"},
		{"zero memory", "$argon2id$v=19$m=0,t=2,p=1$c2FsdHNhbHRzYWx0c2FsdA$aGFzaGhhc2hoYXNoaGFzaGhhc2hoYXNoaGFzaGg"},
		{"a salt that is not base64", "$argon2id$v=19$m=19456,t=2,p=1$!!!!$aGFzaGhhc2hoYXNoaGFzaGhhc2hoYXNoaGFzaGg"},
		{"a salt shorter than this package writes", "$argon2id$v=19$m=19456,t=2,p=1$c2FsdA$aGFzaGhhc2hoYXNoaGFzaGhhc2hoYXNoaGFzaGg"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			err := identitydom.VerifyPassword(c.stored, "any password at all")
			if !errors.Is(err, identitydom.ErrHashUnreadable) {
				t.Errorf("VerifyPassword against %q = %v, want ErrHashUnreadable", c.stored, err)
			}

			if errors.Is(err, identitydom.ErrPasswordMismatch) {
				t.Error("an unreadable hash was reported as a wrong password")
			}

			if !identitydom.NeedsRehash(c.stored) {
				t.Error("an unreadable hash does not ask to be rewritten")
			}
		})
	}
}

// A row asking for a terabyte of memory is not a hash to verify; it is a way
// to take the process down with one login attempt.
func TestARowThatDemandsAbsurdWorkIsRefusedRatherThanObeyed(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		stored string
	}{
		{"a terabyte of memory", "$argon2id$v=19$m=1073741824,t=2,p=1$c2FsdHNhbHRzYWx0c2FsdA$aGFzaGhhc2hoYXNoaGFzaGhhc2hoYXNoaGFzaGg"},
		{"a million passes", "$argon2id$v=19$m=19456,t=1000000,p=1$c2FsdHNhbHRzYWx0c2FsdA$aGFzaGhhc2hoYXNoaGFzaGhhc2hoYXNoaGFzaGg"},
		{"more threads than the machine has", "$argon2id$v=19$m=19456,t=2,p=255$c2FsdHNhbHRzYWx0c2FsdA$aGFzaGhhc2hoYXNoaGFzaGhhc2hoYXNoaGFzaGg"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			if err := identitydom.VerifyPassword(c.stored, "any password at all"); !errors.Is(err, identitydom.ErrHashUnreadable) {
				t.Errorf("VerifyPassword against %q = %v, want ErrHashUnreadable", c.stored, err)
			}
		})
	}
}

// Argon2 has no input limit of its own, so the bound is this package's job.
func TestAPasswordBeyondTheLimitIsRefusedBeforeItIsHashed(t *testing.T) {
	t.Parallel()

	long := strings.Repeat("a", identitydom.MaxPasswordLength+1)

	if _, err := identitydom.HashPassword(long); !errors.Is(err, identitydom.ErrPasswordTooLong) {
		t.Errorf("HashPassword of %d bytes = %v, want ErrPasswordTooLong", len(long), err)
	}

	atLimit := strings.Repeat("a", identitydom.MaxPasswordLength)

	encoded, err := identitydom.HashPassword(atLimit)
	if err != nil {
		t.Fatalf("HashPassword at exactly the limit: %v", err)
	}

	if err := identitydom.VerifyPassword(encoded, atLimit); err != nil {
		t.Errorf("VerifyPassword at exactly the limit: %v", err)
	}

	// An over-long guess is a failed login, not an operator's problem: the
	// hash is fine, the input is not.
	if err := identitydom.VerifyPassword(encoded, long); !errors.Is(err, identitydom.ErrPasswordMismatch) {
		t.Errorf("VerifyPassword with an over-long guess = %v, want ErrPasswordMismatch", err)
	}
}

// A hash written when the cost was lower still verifies, and says it wants
// rewriting. Without both halves, raising the cost would lock every existing
// account out.
func TestAHashFromALowerCostStillVerifiesAndAsksToBeRewritten(t *testing.T) {
	t.Parallel()

	// Written with m=8192,t=1,p=1 — the parameters this package used to use in
	// this test, not the ones it writes now.
	const (
		older    = "$argon2id$v=19$m=8192,t=1,p=1$3gLkNqTjBWFYNjyIn8LCZg$1zJ7pO8nUyC2iZUEgIH0v4Yy0mXKXwvGZzJXK7lLhaE"
		password = "an older password"
	)

	if !identitydom.NeedsRehash(older) {
		t.Error("a hash written with weaker parameters does not ask to be rewritten")
	}

	// The digest above is a fabrication, so it must not verify — but it must
	// fail as a mismatch, proving the weaker parameters were read and used
	// rather than rejected.
	if err := identitydom.VerifyPassword(older, password); !errors.Is(err, identitydom.ErrPasswordMismatch) {
		t.Errorf("VerifyPassword against a weaker hash = %v, want ErrPasswordMismatch", err)
	}
}

// The whole of ADR-0009's requirement on the user: length, counted in
// characters. Nothing about upper case, digits, or symbols — those produce
// Password1! and no extra entropy.
func TestThePolicyIsLengthAndNothingElse(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		password string
		want     error
	}{
		{"one character short", strings.Repeat("a", identitydom.MinPasswordLength-1), identitydom.ErrPasswordTooShort},
		{"exactly at the minimum", strings.Repeat("a", identitydom.MinPasswordLength), nil},
		{"empty", "", identitydom.ErrPasswordTooShort},
		{"long enough and all lower case", "correcthorsebattery", nil},
		{"long enough and all digits", "123456789012", nil},
		{"a passphrase with spaces", "kuda benar baterai steples", nil},
		{"emoji", strings.Repeat("🔐", identitydom.MinPasswordLength), nil},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			if _, err := identitydom.ValidatePassword(c.password); !errors.Is(err, c.want) {
				t.Errorf("ValidatePassword(%q) = %v, want %v", c.password, err, c.want)
			}
		})
	}
}

// A person choosing a password counts characters. Counting bytes would let a
// short CJK password through as though it were long.
func TestLengthIsCountedInCharactersNotBytes(t *testing.T) {
	t.Parallel()

	// Eight characters, twenty-four bytes.
	short := "日本語パスワード"

	if n := len(short); n < identitydom.MinPasswordLength {
		t.Fatalf("this test needs a password whose byte count passes the minimum, got %d bytes", n)
	}

	if _, err := identitydom.ValidatePassword(short); !errors.Is(err, identitydom.ErrPasswordTooShort) {
		t.Errorf("ValidatePassword(%q) = %v, want ErrPasswordTooShort — it is 8 characters", short, err)
	}
}

// The reason normalisation happens before hashing at all. The same password
// typed on a different keyboard arrives as different bytes; without NFKC its
// owner is locked out of half their devices with no explanation.
func TestTheSamePasswordInTwoUnicodeFormsIsOnePassword(t *testing.T) {
	t.Parallel()

	// "café au lait, s'il" — the first spells é as one rune, the second as e
	// followed by a combining acute accent. They look identical on screen.
	const (
		composed   = "caf\u00e9 au lait, s'il"
		decomposed = "cafe\u0301 au lait, s'il"
	)

	if composed == decomposed {
		t.Fatal("this test needs two spellings that differ as bytes")
	}

	encoded, err := identitydom.HashPassword(composed)
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}

	if err := identitydom.VerifyPassword(encoded, decomposed); err != nil {
		t.Errorf("the decomposed spelling does not verify against the composed one: %v", err)
	}

	// And the other way round, since either can be the one that was registered.
	encoded, err = identitydom.HashPassword(decomposed)
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}

	if err := identitydom.VerifyPassword(encoded, composed); err != nil {
		t.Errorf("the composed spelling does not verify against the decomposed one: %v", err)
	}
}

// A password over the limit is refused, never quietly cut down. A truncated
// password is a different password from the one that was typed.
func TestAnOverLongPasswordIsRefusedRatherThanTruncated(t *testing.T) {
	t.Parallel()

	long := strings.Repeat("a", identitydom.MaxPasswordLength+1)

	if _, err := identitydom.ValidatePassword(long); !errors.Is(err, identitydom.ErrPasswordTooLong) {
		t.Fatalf("ValidatePassword of %d bytes = %v, want ErrPasswordTooLong", len(long), err)
	}

	// The limit is far above the 64 characters ADR-0009 asks for, so a
	// password nobody would call short must still be accepted.
	if _, err := identitydom.ValidatePassword(strings.Repeat("a", 64)); err != nil {
		t.Errorf("ValidatePassword of 64 characters = %v, want no error", err)
	}
}
