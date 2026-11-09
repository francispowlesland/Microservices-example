// Package password hashes and verifies passwords with argon2id, storing the
// result as a self-describing PHC string.
package password

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

const (
	saltLen = 16
	keyLen  = 32

	// Upper bounds applied when parsing stored hashes, so a corrupted row
	// cannot make Verify allocate unbounded memory.
	maxMemoryKiB   = 1 << 20
	maxIterations  = 16
	maxParallelism = 64
)

// Params are the argon2id cost parameters. Memory is in KiB.
type Params struct {
	Memory      uint32
	Iterations  uint32
	Parallelism uint8
}

// Default is the production cost: 64 MiB, 3 iterations, parallelism 2.
var Default = Params{Memory: 64 * 1024, Iterations: 3, Parallelism: 2}

// Cheap is for tests only.
var Cheap = Params{Memory: 8, Iterations: 1, Parallelism: 1}

// ErrInvalidHash means the stored hash is not a well-formed argon2id PHC string.
var ErrInvalidHash = errors.New("password: invalid hash encoding")

// Hasher hashes with fixed Params.
type Hasher struct{ p Params }

// NewHasher returns a Hasher using p.
func NewHasher(p Params) *Hasher { return &Hasher{p: p} }

// Hash returns a PHC string: $argon2id$v=19$m=..,t=..,p=..$salt$key.
func (h *Hasher) Hash(pw string) (string, error) {
	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("password: read salt: %w", err)
	}
	key := argon2.IDKey([]byte(pw), salt, h.p.Iterations, h.p.Memory, h.p.Parallelism, keyLen)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, h.p.Memory, h.p.Iterations, h.p.Parallelism,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key)), nil
}

// Verify reports whether pw matches encoded, using the parameters embedded in
// encoded (so hashes made with older costs keep working). The comparison is
// constant-time.
func (h *Hasher) Verify(pw, encoded string) (bool, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" {
		return false, ErrInvalidHash
	}
	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return false, ErrInvalidHash
	}
	var p Params
	var par uint32
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &p.Memory, &p.Iterations, &par); err != nil {
		return false, ErrInvalidHash
	}
	if p.Memory == 0 || p.Memory > maxMemoryKiB || p.Iterations == 0 || p.Iterations > maxIterations ||
		par == 0 || par > maxParallelism {
		return false, ErrInvalidHash
	}
	p.Parallelism = uint8(par)
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil || len(salt) == 0 {
		return false, ErrInvalidHash
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil || len(want) == 0 {
		return false, ErrInvalidHash
	}
	got := argon2.IDKey([]byte(pw), salt, p.Iterations, p.Memory, p.Parallelism, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1, nil
}
