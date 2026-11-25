package argon

import (
	"strings"
	"testing"
)

func TestHashAndVerify(t *testing.T) {
	s := NewSlots(2)
	h, err := s.Hash("correct horse")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(h, "$argon2id$v=19$m=65536,t=2,p=2$") {
		t.Fatalf("unexpected format %q", h)
	}
	if !WellFormed(h) {
		t.Fatal("own hash not well formed")
	}
	if !s.Verify(h, "correct horse") {
		t.Fatal("right password rejected")
	}
	if s.Verify(h, "correct horsE") {
		t.Fatal("wrong password accepted")
	}
}

// A hash written by gpanel (same parameters and encoding) is accepted.
func TestVerifiesGpanelFormat(t *testing.T) {
	// Generated with gpanel's internal/passwd for the password "gpanel-password".
	const h = "$argon2id$v=19$m=65536,t=2,p=2$6IM2O4zQ9qunG8BfXBYmCQ$WiHMXjmHv7vHT6wuNZZT67E4xtv7lbfInnWXayAWmHc"
	if !WellFormed(h) {
		t.Fatal("not well formed")
	}
	s := NewSlots(1)
	if !s.Verify(h, "gpanel-password") {
		t.Fatal("gpanel hash rejected")
	}
}

func TestMalformedNeverMatches(t *testing.T) {
	s := NewSlots(1)
	for _, h := range []string{
		"",
		"plain",
		"$argon2i$v=19$m=65536,t=2,p=2$c2FsdA$a2V5a2V5a2V5a2V5a2V5",
		"$argon2id$v=18$m=65536,t=2,p=2$c2FsdA$a2V5a2V5a2V5a2V5a2V5",
		"$argon2id$v=19$m=4294967295,t=2,p=2$c2FsdA$a2V5a2V5a2V5a2V5a2V5",
		"$argon2id$v=19$m=65536,t=99,p=2$c2FsdA$a2V5a2V5a2V5a2V5a2V5",
		"$argon2id$v=19$m=65536,t=2,p=2$c2FsdA$a2V5",
		"$argon2id$v=19$m=65536,t=2,p=2$!!$a2V5a2V5a2V5a2V5a2V5",
	} {
		if WellFormed(h) || s.Verify(h, "x") {
			t.Errorf("%q accepted", h)
		}
	}
}
