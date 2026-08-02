package crypto

import (
	"bytes"
	"encoding/base64"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testKey(b byte) []byte {
	return bytes.Repeat([]byte{b}, 32)
}

func TestNewEncryptorRejectsBadKeySize(t *testing.T) {
	for _, size := range []int{0, 16, 31, 33, 64} {
		_, err := NewEncryptor(make([]byte, size))
		assert.Error(t, err, "size %d", size)
	}
}

func TestEncryptDecryptRoundtrip(t *testing.T) {
	e, err := NewEncryptor(testKey(1))
	require.NoError(t, err)

	plaintext := []byte(`{"api_key":"secret","address":"0xabc"}`)
	encoded, err := e.Encrypt("record-1", plaintext)
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(encoded, "v1:"))
	assert.NotContains(t, encoded, "secret")

	got, err := e.Decrypt("record-1", encoded)
	require.NoError(t, err)
	assert.Equal(t, plaintext, got)
}

func TestEncryptProducesUniqueCiphertexts(t *testing.T) {
	e, err := NewEncryptor(testKey(1))
	require.NoError(t, err)

	a, err := e.Encrypt("record-1", []byte("payload"))
	require.NoError(t, err)
	b, err := e.Encrypt("record-1", []byte("payload"))
	require.NoError(t, err)
	assert.NotEqual(t, a, b, "nonce must randomize ciphertext")
}

func TestDecryptFailsForWrongRecord(t *testing.T) {
	e, err := NewEncryptor(testKey(1))
	require.NoError(t, err)

	encoded, err := e.Encrypt("record-1", []byte("payload"))
	require.NoError(t, err)

	_, err = e.Decrypt("record-2", encoded)
	assert.ErrorIs(t, err, ErrInvalidCiphertext)
}

func TestDecryptFailsForWrongKey(t *testing.T) {
	e1, err := NewEncryptor(testKey(1))
	require.NoError(t, err)
	e2, err := NewEncryptor(testKey(2))
	require.NoError(t, err)

	encoded, err := e1.Encrypt("record-1", []byte("payload"))
	require.NoError(t, err)

	_, err = e2.Decrypt("record-1", encoded)
	assert.ErrorIs(t, err, ErrInvalidCiphertext)
}

// TestPreviousKeyReadsRowsSealedBeforeRotation is the whole point of the
// fallback: without it, changing the master key makes every encrypted row
// unreadable at once — and the store fails the entire account row on a
// decryption error, so wallet addresses go down with the credentials.
func TestPreviousKeyReadsRowsSealedBeforeRotation(t *testing.T) {
	old, err := NewEncryptor(testKey(1))
	require.NoError(t, err)
	sealedUnderOld, err := old.Encrypt("record-1", []byte(`{"api_key":"secret"}`))
	require.NoError(t, err)

	rotated, err := NewEncryptor(testKey(2))
	require.NoError(t, err)
	rotated, err = rotated.WithPreviousKey(testKey(1))
	require.NoError(t, err)

	got, err := rotated.Decrypt("record-1", sealedUnderOld)
	require.NoError(t, err)
	assert.Equal(t, []byte(`{"api_key":"secret"}`), got)
	assert.True(t, rotated.HasPreviousKey())
}

// TestPreviousKeyIsNeverWrittenWith: the fallback widens reads only. A value
// written after the rotation must not be openable by the retired key, or
// dropping that key later would lose data written after it was retired.
func TestPreviousKeyIsNeverWrittenWith(t *testing.T) {
	rotated, err := NewEncryptor(testKey(2))
	require.NoError(t, err)
	rotated, err = rotated.WithPreviousKey(testKey(1))
	require.NoError(t, err)

	encoded, err := rotated.Encrypt("record-1", []byte("payload"))
	require.NoError(t, err)

	retired, err := NewEncryptor(testKey(1))
	require.NoError(t, err)
	_, err = retired.Decrypt("record-1", encoded)
	assert.ErrorIs(t, err, ErrInvalidCiphertext, "new writes must not be readable by the old key")

	// And the current key alone still opens it once the fallback is dropped.
	current, err := NewEncryptor(testKey(2))
	require.NoError(t, err)
	got, err := current.Decrypt("record-1", encoded)
	require.NoError(t, err)
	assert.Equal(t, []byte("payload"), got)
}

func TestDecryptFailsWhenNoConfiguredKeyOpensTheValue(t *testing.T) {
	sealed, err := mustEncryptor(t, testKey(9)).Encrypt("record-1", []byte("payload"))
	require.NoError(t, err)

	rotated, err := mustEncryptor(t, testKey(2)).WithPreviousKey(testKey(1))
	require.NoError(t, err)

	_, err = rotated.Decrypt("record-1", sealed)
	assert.ErrorIs(t, err, ErrInvalidCiphertext)
}

func TestWithPreviousKeyRejectsBadKeySize(t *testing.T) {
	e := mustEncryptor(t, testKey(1))
	for _, size := range []int{0, 16, 31, 33} {
		_, err := e.WithPreviousKey(make([]byte, size))
		assert.Error(t, err, "size %d", size)
	}
}

func mustEncryptor(t *testing.T, key []byte) *Encryptor {
	t.Helper()
	e, err := NewEncryptor(key)
	require.NoError(t, err)
	return e
}

func TestDecryptRejectsMalformedInput(t *testing.T) {
	e, err := NewEncryptor(testKey(1))
	require.NoError(t, err)

	for name, encoded := range map[string]string{
		"unknown version": "v9:AAAA",
		"no prefix":       "AAAA",
		"bad base64":      "v1:!!!not-base64!!!",
		"too short":       "v1:AAAA",
		"empty":           "",
	} {
		t.Run(name, func(t *testing.T) {
			_, err := e.Decrypt("record-1", encoded)
			assert.ErrorIs(t, err, ErrInvalidCiphertext)
		})
	}
}

func TestDecryptRejectsTamperedCiphertext(t *testing.T) {
	e, err := NewEncryptor(testKey(1))
	require.NoError(t, err)

	encoded, err := e.Encrypt("record-1", []byte("payload"))
	require.NoError(t, err)

	// Flip a bit in the raw sealed bytes (the GCM tag) rather than in the
	// base64 text: a base64 character near padding can have low bits that
	// decode to the same byte, making a textual flip a silent no-op.
	raw, ok := strings.CutPrefix(encoded, "v1:")
	require.True(t, ok)
	sealed, err := base64.StdEncoding.DecodeString(raw)
	require.NoError(t, err)
	sealed[len(sealed)-1] ^= 0x01 // corrupt the last tag byte
	tampered := "v1:" + base64.StdEncoding.EncodeToString(sealed)

	_, err = e.Decrypt("record-1", tampered)
	assert.ErrorIs(t, err, ErrInvalidCiphertext)
}
