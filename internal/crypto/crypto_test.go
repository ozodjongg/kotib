package crypto

import (
	"bytes"
	"crypto/rand"
	"testing"
)

func newTestVault(t *testing.T) *Vault {
	t.Helper()
	k := make([]byte, 32)
	if _, err := rand.Read(k); err != nil {
		t.Fatal(err)
	}
	return NewVault(&StaticKeys{CurrentID: 1, Keys: map[uint8][]byte{1: k}})
}

func TestSealOpenRoundTrip(t *testing.T) {
	v := newTestVault(t)
	msg := []byte("session-bytes")
	blob, err := v.Seal(msg, []byte("user:42"))
	if err != nil {
		t.Fatal(err)
	}
	got, err := v.Open(blob, []byte("user:42"))
	if err != nil || !bytes.Equal(got, msg) {
		t.Fatalf("round trip: %v %q", err, got)
	}
}

func TestOpenWrongAADFails(t *testing.T) {
	v := newTestVault(t)
	blob, _ := v.Seal([]byte("secret"), []byte("user:1"))
	if _, err := v.Open(blob, []byte("user:2")); err == nil {
		t.Fatal("boshqa foydalanuvchi aad'i bilan ochilmasligi kerak edi")
	}
}

func TestOpenTamperedFails(t *testing.T) {
	v := newTestVault(t)
	blob, _ := v.Seal([]byte("secret"), nil)
	blob[len(blob)-1] ^= 0xff
	if _, err := v.Open(blob, nil); err == nil {
		t.Fatal("buzilgan blob ochilmasligi kerak edi")
	}
}

func TestKeyRotation(t *testing.T) {
	k1, k2 := make([]byte, 32), make([]byte, 32)
	rand.Read(k1)
	rand.Read(k2)
	old := NewVault(&StaticKeys{CurrentID: 1, Keys: map[uint8][]byte{1: k1}})
	blob, _ := old.Seal([]byte("x"), nil)

	rotated := NewVault(&StaticKeys{CurrentID: 2, Keys: map[uint8][]byte{1: k1, 2: k2}})
	if _, err := rotated.Open(blob, nil); err != nil {
		t.Fatalf("eski kalit bilan yozilgan blob ochilishi kerak: %v", err)
	}
	nb, _ := rotated.Seal([]byte("y"), nil)
	if nb[1] != 2 {
		t.Fatalf("yangi blob kid=2 bo'lishi kerak, %d", nb[1])
	}
}
