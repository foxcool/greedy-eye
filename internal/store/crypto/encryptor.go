// Package crypto provides application-level encryption for sensitive store
// payloads, as described in ADR-005 (docs/architecture.md §9).
package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
)

const (
	// versionPrefix tags the on-disk format for future rotation.
	versionPrefix = "v1:"
	// keyInfoPrefix namespaces per-record derived keys.
	keyInfoPrefix = "greedy-eye/accounts/"

	masterKeySize = 32
	nonceSize     = 12
)

// ErrInvalidCiphertext is returned when an encoded value cannot be decoded or
// fails authentication (wrong key, wrong record, or tampered data).
var ErrInvalidCiphertext = errors.New("invalid ciphertext")

// Encryptor encrypts payloads with AES-256-GCM using per-record keys derived
// from a master key via HKDF-SHA256. Ciphertext is bound to the record ID:
// a value encrypted for one record does not decrypt for another.
type Encryptor struct {
	masterKey []byte
}

// NewEncryptor creates an Encryptor from a 32-byte master key.
func NewEncryptor(masterKey []byte) (*Encryptor, error) {
	if len(masterKey) != masterKeySize {
		return nil, fmt.Errorf("master key must be %d bytes, got %d", masterKeySize, len(masterKey))
	}
	return &Encryptor{masterKey: masterKey}, nil
}

func (e *Encryptor) gcm(recordID string) (cipher.AEAD, error) {
	key, err := hkdf.Key(sha256.New, e.masterKey, nil, keyInfoPrefix+recordID, masterKeySize)
	if err != nil {
		return nil, fmt.Errorf("derive key: %w", err)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("init cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("init GCM: %w", err)
	}
	return aead, nil
}

// Encrypt seals plaintext for the given record and returns the versioned,
// base64-encoded value ("v1:<base64(nonce || ciphertext)>").
func (e *Encryptor) Encrypt(recordID string, plaintext []byte) (string, error) {
	aead, err := e.gcm(recordID)
	if err != nil {
		return "", err
	}

	nonce := make([]byte, nonceSize)
	if _, err := rand.Read(nonce); err != nil {
		return "", fmt.Errorf("generate nonce: %w", err)
	}

	sealed := aead.Seal(nonce, nonce, plaintext, nil)
	return versionPrefix + base64.StdEncoding.EncodeToString(sealed), nil
}

// Decrypt opens a value produced by Encrypt for the same record.
func (e *Encryptor) Decrypt(recordID, encoded string) ([]byte, error) {
	raw, ok := strings.CutPrefix(encoded, versionPrefix)
	if !ok {
		return nil, fmt.Errorf("%w: unknown format version", ErrInvalidCiphertext)
	}

	sealed, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidCiphertext, err)
	}
	if len(sealed) < nonceSize {
		return nil, fmt.Errorf("%w: too short", ErrInvalidCiphertext)
	}

	aead, err := e.gcm(recordID)
	if err != nil {
		return nil, err
	}

	plaintext, err := aead.Open(nil, sealed[:nonceSize], sealed[nonceSize:], nil)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidCiphertext, err)
	}
	return plaintext, nil
}
