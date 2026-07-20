package cosmos

import (
	"errors"
	"strings"
)

// Cosmos addresses are bech32 over a chain-specific human-readable part and one
// shared payload: the same key is "cosmos1…" on the hub and "akash1…" on Akash,
// with only the prefix and checksum differing.
//
// Subscan resolves any network's form of a Substrate address server-side, so
// that adapter sends one string everywhere. Cosmos has no such service — each
// chain runs its own LCD and accepts only its own prefix — so re-encoding is
// this adapter's job.

const charset = "qpzry9x8gf2tvdw0s3jn54khce6mua7l"

var charsetIndex = func() [256]int8 {
	var t [256]int8
	for i := range t {
		t[i] = -1
	}
	for i := range len(charset) {
		t[charset[i]] = int8(i)
	}
	return t
}()

var (
	errBech32Format   = errors.New("bech32: malformed address")
	errBech32Checksum = errors.New("bech32: bad checksum")
)

// decodeBech32 splits an address into its prefix and its payload, which stays
// in the 5-bit form bech32 stores it in — re-encoding under another prefix
// needs no conversion, only a new checksum.
func decodeBech32(address string) (hrp string, data []byte, err error) {
	if len(address) < 8 || len(address) > 90 {
		return "", nil, errBech32Format
	}

	// Mixed case carries no meaning and cannot be checksummed either way.
	lower := strings.ToLower(address)
	if address != lower && address != strings.ToUpper(address) {
		return "", nil, errBech32Format
	}
	address = lower

	// The separator is the last '1': the prefix may contain one, the payload
	// alphabet may not.
	sep := strings.LastIndexByte(address, '1')
	if sep < 1 || sep+7 > len(address) {
		return "", nil, errBech32Format
	}

	hrp = address[:sep]
	for i := range len(hrp) {
		if hrp[i] < 33 || hrp[i] > 126 {
			return "", nil, errBech32Format
		}
	}

	payload := address[sep+1:]
	data = make([]byte, len(payload))
	for i := range len(payload) {
		v := charsetIndex[payload[i]]
		if v < 0 {
			return "", nil, errBech32Format
		}
		data[i] = byte(v)
	}

	if polymod(append(hrpExpand(hrp), data...)) != 1 {
		return "", nil, errBech32Checksum
	}
	return hrp, data[:len(data)-6], nil
}

// encodeBech32 renders a 5-bit payload under a prefix, computing the checksum
// that prefix requires.
func encodeBech32(hrp string, data []byte) string {
	values := append(hrpExpand(hrp), data...)
	checksum := createChecksum(values)

	var b strings.Builder
	b.WriteString(hrp)
	b.WriteByte('1')
	for _, v := range append(data, checksum...) {
		b.WriteByte(charset[v])
	}
	return b.String()
}

// reencode moves an address to another chain's prefix, which is the whole of
// what distinguishes one Cosmos chain's address from another's.
func reencode(address, hrp string) (string, error) {
	_, data, err := decodeBech32(address)
	if err != nil {
		return "", err
	}
	return encodeBech32(hrp, data), nil
}

func createChecksum(values []byte) []byte {
	values = append(values, 0, 0, 0, 0, 0, 0)
	mod := polymod(values) ^ 1

	checksum := make([]byte, 6)
	for i := range 6 {
		// Mask to five bits before narrowing: the checksum is six base32
		// digits, and converting first would rely on the truncation to do it.
		checksum[i] = byte((mod >> uint(5*(5-i))) & 31)
	}
	return checksum
}

func hrpExpand(hrp string) []byte {
	out := make([]byte, 0, len(hrp)*2+1)
	for i := range len(hrp) {
		out = append(out, hrp[i]>>5)
	}
	out = append(out, 0)
	for i := range len(hrp) {
		out = append(out, hrp[i]&31)
	}
	return out
}

func polymod(values []byte) uint32 {
	gen := [5]uint32{0x3b6a57b2, 0x26508e6d, 0x1ea119fa, 0x3d4233dd, 0x2a1462b3}
	chk := uint32(1)
	for _, v := range values {
		top := chk >> 25
		chk = (chk&0x1ffffff)<<5 ^ uint32(v)
		for i := range 5 {
			if (top>>uint(i))&1 == 1 {
				chk ^= gen[i]
			}
		}
	}
	return chk
}
