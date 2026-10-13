// Package crypto: session va maxfiy ma'lumotlarni envelope-shifrlash.
//
// Sxema: har bir yozuv uchun tasodifiy DEK (data key) yaratiladi, ma'lumot
// DEK bilan AES-256-GCM orqali shifrlanadi, DEK esa master kalit (KEK) bilan
// o'ralib (wrap) birga saqlanadi. KEK bazada emas — env/KMS'dan keladi.
// Kalit almashtirish (rotation) uchun KEK'ga versiya (kid) biriktiriladi.
package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
)

const (
	keySize = 32
	version = byte(1)
)

// KeyProvider master kalitlarni beradi (env, Vault, cloud KMS va h.k.).
type KeyProvider interface {
	// Current joriy KEK va uning identifikatorini qaytaradi (yangi yozuvlar uchun).
	Current() (kid uint8, key []byte, err error)
	// Get eski yozuvlarni ochish uchun kid bo'yicha KEK qaytaradi.
	Get(kid uint8) ([]byte, error)
}

// StaticKeys — oddiy KeyProvider (env'dan olingan kalitlar uchun).
type StaticKeys struct {
	CurrentID uint8
	Keys      map[uint8][]byte
}

// NewStaticKeyFromBase64 bitta base64 (32 bayt) kalitdan provider yasaydi.
func NewStaticKeyFromBase64(b64 string) (*StaticKeys, error) {
	k, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return nil, fmt.Errorf("master key base64: %w", err)
	}
	if len(k) != keySize {
		return nil, fmt.Errorf("master key %d bayt bo'lishi kerak, %d berildi", keySize, len(k))
	}
	return &StaticKeys{CurrentID: 1, Keys: map[uint8][]byte{1: k}}, nil
}

func (s *StaticKeys) Current() (uint8, []byte, error) {
	k, ok := s.Keys[s.CurrentID]
	if !ok {
		return 0, nil, errors.New("joriy kalit topilmadi")
	}
	return s.CurrentID, k, nil
}

func (s *StaticKeys) Get(kid uint8) ([]byte, error) {
	k, ok := s.Keys[kid]
	if !ok {
		return nil, fmt.Errorf("kalit topilmadi: kid=%d", kid)
	}
	return k, nil
}

// Vault shifrlash/ochish amallarini bajaradi.
type Vault struct{ kp KeyProvider }

func NewVault(kp KeyProvider) *Vault { return &Vault{kp: kp} }

// Seal plaintext'ni shifrlaydi. aad (associated data) — masalan, "user:<id>":
// shifrlangan blobni boshqa foydalanuvchi qatoriga ko'chirib bo'lmaydi.
//
// Format: ver(1) | kid(1) | wrapNonce(12) | wrappedDEK(32+16) | dataNonce(12) | ciphertext
func (v *Vault) Seal(plaintext, aad []byte) ([]byte, error) {
	kid, kek, err := v.kp.Current()
	if err != nil {
		return nil, err
	}
	dek := make([]byte, keySize)
	if _, err := io.ReadFull(rand.Reader, dek); err != nil {
		return nil, err
	}
	defer zero(dek)

	wrapNonce, wrapped, err := gcmSeal(kek, dek, aad)
	if err != nil {
		return nil, err
	}
	dataNonce, ct, err := gcmSeal(dek, plaintext, aad)
	if err != nil {
		return nil, err
	}

	out := make([]byte, 0, 2+len(wrapNonce)+len(wrapped)+len(dataNonce)+len(ct))
	out = append(out, version, kid)
	out = append(out, wrapNonce...)
	out = append(out, wrapped...)
	out = append(out, dataNonce...)
	out = append(out, ct...)
	return out, nil
}

// Open Seal natijasini ochadi. aad Seal'dagi bilan bir xil bo'lishi shart.
func (v *Vault) Open(blob, aad []byte) ([]byte, error) {
	const nonceLen = 12
	const wrappedLen = keySize + 16
	if len(blob) < 2+nonceLen+wrappedLen+nonceLen+16 {
		return nil, errors.New("blob juda qisqa")
	}
	if blob[0] != version {
		return nil, fmt.Errorf("noma'lum versiya: %d", blob[0])
	}
	kek, err := v.kp.Get(blob[1])
	if err != nil {
		return nil, err
	}
	p := 2
	wrapNonce := blob[p : p+nonceLen]
	p += nonceLen
	wrapped := blob[p : p+wrappedLen]
	p += wrappedLen
	dataNonce := blob[p : p+nonceLen]
	p += nonceLen
	ct := blob[p:]

	dek, err := gcmOpen(kek, wrapNonce, wrapped, aad)
	if err != nil {
		return nil, errors.New("DEK ochilmadi (kalit yoki aad noto'g'ri)")
	}
	defer zero(dek)
	pt, err := gcmOpen(dek, dataNonce, ct, aad)
	if err != nil {
		return nil, errors.New("ma'lumot ochilmadi (buzilgan yoki aad noto'g'ri)")
	}
	return pt, nil
}

func gcmSeal(key, plaintext, aad []byte) (nonce, ct []byte, err error) {
	g, err := newGCM(key)
	if err != nil {
		return nil, nil, err
	}
	nonce = make([]byte, g.NonceSize())
	if _, err = io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, nil, err
	}
	return nonce, g.Seal(nil, nonce, plaintext, aad), nil
}

func gcmOpen(key, nonce, ct, aad []byte) ([]byte, error) {
	g, err := newGCM(key)
	if err != nil {
		return nil, err
	}
	return g.Open(nil, nonce, ct, aad)
}

func newGCM(key []byte) (cipher.AEAD, error) {
	if len(key) != keySize {
		return nil, errors.New("kalit uzunligi 32 bayt bo'lishi kerak")
	}
	b, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(b)
}

func zero(b []byte) {
	for i := range b {
		b[i] = 0
	}
}
