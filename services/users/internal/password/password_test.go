package password

import (
	"strings"
	"testing"
)

func TestHashVerify(t *testing.T) {
	h := NewHasher(Cheap)
	enc, err := h.Hash("correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(enc, "$argon2id$v=19$m=8,t=1,p=1$") {
		t.Fatalf("unexpected PHC prefix: %q", enc)
	}
	tests := []struct {
		name, pw string
		want     bool
	}{
		{"match", "correct horse battery", true},
		{"mismatch", "correct horse batterY", false},
		{"empty", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := h.Verify(tt.pw, enc)
			if err != nil || got != tt.want {
				t.Fatalf("Verify = %v, %v; want %v, nil", got, err, tt.want)
			}
		})
	}
}

func TestHashIsSalted(t *testing.T) {
	h := NewHasher(Cheap)
	a, _ := h.Hash("same password here")
	b, _ := h.Hash("same password here")
	if a == b {
		t.Fatal("two hashes of the same password are identical")
	}
}

func TestVerifyUsesEmbeddedParams(t *testing.T) {
	enc, _ := NewHasher(Cheap).Hash("another password!!")
	ok, err := NewHasher(Default).Verify("another password!!", enc)
	if err != nil || !ok {
		t.Fatalf("Verify with different hasher params = %v, %v", ok, err)
	}
}

func TestVerifyMalformed(t *testing.T) {
	h := NewHasher(Cheap)
	for _, enc := range []string{
		"", "plain", "$argon2i$v=19$m=8,t=1,p=1$c2FsdA$a2V5",
		"$argon2id$v=18$m=8,t=1,p=1$c2FsdA$a2V5",
		"$argon2id$v=19$m=0,t=1,p=1$c2FsdA$a2V5",
		"$argon2id$v=19$m=8,t=1,p=1$!!$a2V5",
		"$argon2id$v=19$m=99999999,t=1,p=1$c2FsdA$a2V5",
	} {
		if ok, err := h.Verify("x", enc); ok || err == nil {
			t.Errorf("Verify(%q) = %v, %v; want false, error", enc, ok, err)
		}
	}
}
