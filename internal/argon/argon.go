// Package argon hashes and verifies passwords with argon2id. The encoded
// format ($argon2id$v=19$m=…,t=…,p=…$salt$key) is the one gpanel uses, so
// hashes can be moved between the two without resetting passwords.
package argon

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

// Parameters for new hashes. Verification reads the parameters from the
// stored hash, so these can be raised later.
const (
	argonTime    = 2
	argonMemory  = 64 * 1024 // KiB
	argonThreads = 2
	argonSaltLen = 16
	argonKeyLen  = 32
)

// Slots bounds concurrent hashing in one process: each hash takes 64 MiB.
type Slots chan struct{}

// NewSlots returns room for n hashes at the same time.
func NewSlots(n int) Slots { return make(Slots, n) }

// Hash returns an encoded argon2id hash of pw.
func (s Slots) Hash(pw string) (string, error) {
	salt := make([]byte, argonSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	s <- struct{}{}
	key := argon2.IDKey([]byte(pw), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	<-s
	b64 := base64.RawStdEncoding
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, argonMemory, argonTime, argonThreads, b64.EncodeToString(salt), b64.EncodeToString(key)), nil
}

// Verify compares pw with an encoded argon2id hash in constant time.
// Malformed hashes never match.
func (s Slots) Verify(encoded, pw string) bool {
	p, ok := parse(encoded)
	if !ok {
		return false
	}
	s <- struct{}{}
	got := argon2.IDKey([]byte(pw), p.salt, p.time, p.memory, p.threads, uint32(len(p.key)))
	<-s
	return subtle.ConstantTimeCompare(got, p.key) == 1
}

// WellFormed tells whether encoded is a hash Verify can check, without
// computing anything.
func WellFormed(encoded string) bool {
	_, ok := parse(encoded)
	return ok
}

type params struct {
	memory, time uint32
	threads      uint8
	salt, key    []byte
}

func parse(encoded string) (params, bool) {
	var p params
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" {
		return p, false
	}
	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return p, false
	}
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &p.memory, &p.time, &p.threads); err != nil {
		return p, false
	}
	// Bounds keep a corrupted hash from exhausting memory or CPU.
	if p.memory < 8 || p.memory > 1<<20 || p.time < 1 || p.time > 16 || p.threads < 1 {
		return p, false
	}
	b64 := base64.RawStdEncoding
	var err error
	if p.salt, err = b64.DecodeString(parts[4]); err != nil {
		return p, false
	}
	if p.key, err = b64.DecodeString(parts[5]); err != nil || len(p.key) < 16 || len(p.key) > 64 {
		return p, false
	}
	return p, true
}
