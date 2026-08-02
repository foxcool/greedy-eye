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

// TestStaleKeysReadRowsSealedBeforeRotation is the whole point of the key list:
// without it, changing the master key makes every encrypted row unreadable at
// once — and the store fails the entire account row on a decryption error, so
// wallet addresses go down with the credentials.
func TestStaleKeysReadRowsSealedBeforeRotation(t *testing.T) {
	old := mustEncryptor(t, testKey(1))
	sealedUnderOld, err := old.Encrypt("record-1", []byte(`{"api_key":"secret"}`))
	require.NoError(t, err)

	rotated := mustEncryptor(t, testKey(2), testKey(1))

	got, err := rotated.Decrypt("record-1", sealedUnderOld)
	require.NoError(t, err)
	assert.Equal(t, []byte(`{"api_key":"secret"}`), got)
	assert.Equal(t, 1, rotated.StaleKeys())
}

// TestReadsReachEveryGenerationInTheList: more than one rotation can be in
// flight, and a row from two keys ago must not become unreadable because a third
// was prepended.
func TestReadsReachEveryGenerationInTheList(t *testing.T) {
	oldest, err := mustEncryptor(t, testKey(1)).Encrypt("record-1", []byte("ancient"))
	require.NoError(t, err)
	middle, err := mustEncryptor(t, testKey(2)).Encrypt("record-1", []byte("recent"))
	require.NoError(t, err)

	e := mustEncryptor(t, testKey(3), testKey(2), testKey(1))
	assert.Equal(t, 2, e.StaleKeys())

	got, err := e.Decrypt("record-1", oldest)
	require.NoError(t, err)
	assert.Equal(t, []byte("ancient"), got)
	got, err = e.Decrypt("record-1", middle)
	require.NoError(t, err)
	assert.Equal(t, []byte("recent"), got)
}

// TestOnlyTheFirstKeyIsWrittenWith: the list widens reads only. A value written
// after the rotation must not be openable by a retired key, or dropping that key
// later would lose data written after it was retired.
func TestOnlyTheFirstKeyIsWrittenWith(t *testing.T) {
	rotated := mustEncryptor(t, testKey(2), testKey(1))

	encoded, err := rotated.Encrypt("record-1", []byte("payload"))
	require.NoError(t, err)

	_, err = mustEncryptor(t, testKey(1)).Decrypt("record-1", encoded)
	assert.ErrorIs(t, err, ErrInvalidCiphertext, "new writes must not be readable by a retired key")

	// And the current key alone still opens it once the tail is dropped.
	got, err := rotated.Current().Decrypt("record-1", encoded)
	require.NoError(t, err)
	assert.Equal(t, []byte("payload"), got)
}

// TestCurrentDropsTheStaleKeys: the rekey job uses Current() to answer the
// question counters cannot — is every row readable without the stale keys?
func TestCurrentDropsTheStaleKeys(t *testing.T) {
	sealedUnderOld, err := mustEncryptor(t, testKey(1)).Encrypt("record-1", []byte("payload"))
	require.NoError(t, err)

	rotated := mustEncryptor(t, testKey(2), testKey(1))
	current := rotated.Current()

	assert.Equal(t, 0, current.StaleKeys())
	_, err = current.Decrypt("record-1", sealedUnderOld)
	assert.ErrorIs(t, err, ErrInvalidCiphertext, "a row still under the old key must fail the check")
}

func TestDecryptFailsWhenNoConfiguredKeyOpensTheValue(t *testing.T) {
	sealed, err := mustEncryptor(t, testKey(9)).Encrypt("record-1", []byte("payload"))
	require.NoError(t, err)

	_, err = mustEncryptor(t, testKey(2), testKey(1)).Decrypt("record-1", sealed)
	assert.ErrorIs(t, err, ErrInvalidCiphertext)
}

// TestNewEncryptorRejectsDuplicateKeys: the same key twice is not a rotation in
// progress, it is a config edit gone wrong — and it would make the rekey job
// claim a stale key exists when none does.
func TestNewEncryptorRejectsDuplicateKeys(t *testing.T) {
	_, err := NewEncryptor(testKey(1), testKey(1))
	assert.Error(t, err)
}

func TestNewEncryptorRejectsEmptyKeyList(t *testing.T) {
	_, err := NewEncryptor()
	assert.Error(t, err)
}

func TestNewEncryptorRejectsBadStaleKeySize(t *testing.T) {
	for _, size := range []int{0, 16, 31, 33} {
		_, err := NewEncryptor(testKey(1), make([]byte, size))
		assert.Error(t, err, "size %d", size)
	}
}

func mustEncryptor(t *testing.T, keys ...[]byte) *Encryptor {
	t.Helper()
	e, err := NewEncryptor(keys...)
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
