package base58

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestDecodeLengths is the property the address predicates rely on: a decoded
// length that distinguishes formats sharing the base58 alphabet.
func TestDecodeLengths(t *testing.T) {
	tests := []struct {
		name    string
		address string
		wantLen int
	}{
		// 1-byte network prefix + 32-byte key + 2-byte checksum.
		{"ss58 generic", "5DsvsaNbaA4JPXPRJHA2wWDMv4oJaWKQDbrUKEeELRfrto7Q", 35},
		{"ss58 polkadot", "12pE1udfRwKmq4PwFvD35f3WmgnxGosYJ6axUXdatWhP5TUm", 35},
		// A bare 32-byte public key, no prefix and no checksum.
		{"solana pubkey", "CvC1oRhFemouyXTBSJp6NBdEUN1CqQEYRNFcUh46Cqv8", 32},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			decoded, err := Decode(tt.address)
			require.NoError(t, err)
			assert.Len(t, decoded, tt.wantLen)
		})
	}
}

// TestDecodeLeadingZeros: leading '1's encode zero bytes, and dropping them
// would shorten the decoded payload and break every length check downstream.
func TestDecodeLeadingZeros(t *testing.T) {
	decoded, err := Decode("11" + "z")
	require.NoError(t, err)
	assert.Equal(t, []byte{0x00, 0x00, 57}, decoded)
}

func TestDecodeRejectsForeignAlphabet(t *testing.T) {
	for _, s := range []string{"0OIl", "hello world", "bc1qar0srrr"} {
		_, err := Decode(s)
		assert.ErrorIs(t, err, ErrInvalidCharacter, s)
	}
}

func TestDecodeEmpty(t *testing.T) {
	decoded, err := Decode("")
	require.NoError(t, err)
	assert.Empty(t, decoded)
}
