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
//
// It may hold a previous master key as well. Writes always use the current one;
// reads fall back to the previous, so a rotation does not have to be atomic
// with the re-encryption of every row. Without that fallback, changing the key
// makes every encrypted row unreadable at once — and because the store fails
// the whole account row on a decryption error, that takes wallet addresses down
// with the credentials.
type Encryptor struct {
	masterKey   []byte
	previousKey []byte
}

// NewEncryptor creates an Encryptor from a 32-byte master key.
func NewEncryptor(masterKey []byte) (*Encryptor, error) {
	if len(masterKey) != masterKeySize {
		return nil, fmt.Errorf("master key must be %d bytes, got %d", masterKeySize, len(masterKey))
	}
	return &Encryptor{masterKey: masterKey}, nil
}

// WithPreviousKey returns an Encryptor that also reads values sealed under an
// earlier master key. One generation back is enough: a rotation is finished by
// re-encrypting the rows (PortfolioStore.RewrapAccountData), and keeping a
// longer chain would let an operator lose track of which keys are still load
// bearing.
func (e *Encryptor) WithPreviousKey(previous []byte) (*Encryptor, error) {
	if len(previous) != masterKeySize {
		return nil, fmt.Errorf("previous master key must be %d bytes, got %d", masterKeySize, len(previous))
	}
	return &Encryptor{masterKey: e.masterKey, previousKey: previous}, nil
}

// HasPreviousKey reports whether a fallback key is configured, so a caller can
// refuse to run a rotation that has nothing to read the old rows with.
func (e *Encryptor) HasPreviousKey() bool { return len(e.previousKey) > 0 }

func (e *Encryptor) gcm(recordID string) (cipher.AEAD, error) {
	return gcmForKey(e.masterKey, recordID)
}

func gcmForKey(masterKey []byte, recordID string) (cipher.AEAD, error) {
	key, err := hkdf.Key(sha256.New, masterKey, nil, keyInfoPrefix+recordID, masterKeySize)
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

// Decrypt opens a value produced by Encrypt for the same record, trying the
// current master key first and the previous one after — a row sealed before a
// rotation stays readable until the rewrap pass reaches it.
//
// Which key opened the value is deliberately not reported: nothing on the read
// path should behave differently, and a caller that needs to know is really
// asking to re-encrypt, which RewrapAccountData does unconditionally.
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

	keys := [][]byte{e.masterKey}
	if len(e.previousKey) > 0 {
		keys = append(keys, e.previousKey)
	}
	for _, key := range keys {
		aead, err := gcmForKey(key, recordID)
		if err != nil {
			return nil, err
		}
		plaintext, err := aead.Open(nil, sealed[:nonceSize], sealed[nonceSize:], nil)
		if err == nil {
			return plaintext, nil
		}
	}
	return nil, fmt.Errorf("%w: authentication failed under every configured master key", ErrInvalidCiphertext)
}
