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
