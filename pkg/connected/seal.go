package connected

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
)

// Purposes of sealed values. Each one gets its own key, so a value sealed for one
// purpose never opens for another.
const (
	PurposeToken        = "token"
	PurposeClientSecret = "client-secret"
	PurposeGrant        = "grant"
)

const (
	sealVersion = 1
	nonceSize   = 12
	keyInfo     = "fylvo connected storage v1 "
)

// ErrSealed is returned for a value that does not open: wrong key, wrong context
// or changed bytes. It says nothing about which.
var ErrSealed = errors.New("sealed value cannot be opened")

// MasterKey returns the master key of the installation.
type MasterKey func(ctx context.Context) ([]byte, error)

// Sealer encrypts small secrets for the database and for grants.
// Format: version (1 byte) | nonce (12 bytes) | AES-256-GCM ciphertext with tag.
type Sealer struct {
	master MasterKey
}

func NewSealer(master MasterKey) *Sealer {
	return &Sealer{master: master}
}

func (s *Sealer) aead(ctx context.Context, purpose string) (cipher.AEAD, error) {
	master, err := s.master(ctx)
	if err != nil {
		return nil, fmt.Errorf("master key: %w", err)
	}
	if len(master) < 16 {
		return nil, errors.New("master key is too short")
	}
	key, err := hkdf.Key(sha256.New, master, nil, keyInfo+purpose, 32)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

// Seal encrypts plain. context binds the result to where it belongs (see Context):
// it opens only with the same context.
func (s *Sealer) Seal(ctx context.Context, purpose string, context, plain []byte) ([]byte, error) {
	aead, err := s.aead(ctx, purpose)
	if err != nil {
		return nil, err
	}
	out := make([]byte, 1+nonceSize, 1+nonceSize+len(plain)+aead.Overhead())
	out[0] = sealVersion
	if _, err := rand.Read(out[1:]); err != nil {
		return nil, err
	}
	return aead.Seal(out, out[1:1+nonceSize], plain, context), nil
}

// Open decrypts what Seal made.
func (s *Sealer) Open(ctx context.Context, purpose string, context, sealed []byte) ([]byte, error) {
	if len(sealed) < 1+nonceSize || sealed[0] != sealVersion {
		return nil, ErrSealed
	}
	aead, err := s.aead(ctx, purpose)
	if err != nil {
		return nil, err
	}
	plain, err := aead.Open(nil, sealed[1:1+nonceSize], sealed[1+nonceSize:], context)
	if err != nil {
		return nil, ErrSealed
	}
	return plain, nil
}

// Context joins the fields a sealed value is bound to. Each field is prefixed with
// its length, so no two lists of fields give the same bytes.
func Context(fields ...string) []byte {
	var out []byte
	for _, f := range fields {
		out = binary.BigEndian.AppendUint32(out, uint32(len(f)))
		out = append(out, f...)
	}
	return out
}
