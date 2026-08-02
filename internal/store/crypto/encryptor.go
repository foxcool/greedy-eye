// Package crypto provides application-level encryption for sensitive store
// payloads, as described in ADR-005 (docs/architecture.md §9).
package crypto

import (
	"bytes"
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
// It holds one or more master keys. The FIRST is current and the only one ever
// written with; the rest are stale keys kept so rows sealed before a rotation
// stay readable until the rekey job reaches them. Without that fallback,
// changing the key makes every encrypted row unreadable at once — and because
// the store fails the whole account row on a decryption error, that takes
// wallet addresses down with the credentials.
type Encryptor struct {
	// keys[0] is current; keys[1:] are stale, read-only.
	keys [][]byte
}

// NewEncryptor creates an Encryptor from one or more 32-byte master keys. The
// first is the current one; any others are accepted on read only.
//
// Order is the whole contract here, so it is validated rather than trusted: an
// operator who prepends the retired key instead of the new one would otherwise
// silently start writing with the key they are trying to get rid of.
func NewEncryptor(keys ...[]byte) (*Encryptor, error) {
	if len(keys) == 0 {
		return nil, fmt.Errorf("at least one master key is required")
	}
	for i, key := range keys {
		if len(key) != masterKeySize {
			return nil, fmt.Errorf("master key %d must be %d bytes, got %d", i, masterKeySize, len(key))
		}
	}
	for i := range keys {
		for j := i + 1; j < len(keys); j++ {
			if bytes.Equal(keys[i], keys[j]) {
				return nil, fmt.Errorf("master keys %d and %d are identical", i, j)
			}
		}
	}
	return &Encryptor{keys: keys}, nil
}

// Current returns an Encryptor holding only the current key. The rekey job uses
// it to answer the question the counters cannot: is every row now readable
// without the stale keys, so they can be dropped?
func (e *Encryptor) Current() *Encryptor {
	return &Encryptor{keys: e.keys[:1]}
}

// StaleKeys is how many read-only keys are configured behind the current one.
// Zero means the rotation is finished as far as configuration is concerned.
func (e *Encryptor) StaleKeys() int { return len(e.keys) - 1 }

func (e *Encryptor) gcm(recordID string) (cipher.AEAD, error) {
	return gcmForKey(e.keys[0], recordID)
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
// current master key first and the stale ones after — a row sealed before a
// rotation stays readable until the rekey job reaches it.
//
// Which key opened the value is deliberately not reported, and nothing is
// re-encrypted here. Rewriting a row on the read path would turn every list
// into a burst of writes, break on a read-only replica, and still never
// converge: a row nobody reads is a row nobody rewrites, so it could never
// answer whether a stale key is safe to drop. That question belongs to the
// rekey job, which walks every row.
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

	for _, key := range e.keys {
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
