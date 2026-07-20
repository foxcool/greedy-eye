// Package base58 decodes the Bitcoin base58 alphabet, shared by the adapters
// whose address formats are built on it (SS58 on Substrate, Solana pubkeys).
//
// Decoding exists here rather than in a regexp because those two formats are
// only two characters apart in length over an identical alphabet: telling them
// apart by shape alone risks routing an address to the wrong chain, which reads
// back as an empty wallet and silently zeroes a position.
package base58

import (
	"errors"
	"math/big"
)

const alphabet = "123456789ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz"

// index maps a byte to its base58 digit, or -1 when the byte is not in the
// alphabet.
var index = func() [256]int8 {
	var t [256]int8
	for i := range t {
		t[i] = -1
	}
	for i := range len(alphabet) {
		t[alphabet[i]] = int8(i)
	}
	return t
}()

// ErrInvalidCharacter reports a byte outside the base58 alphabet.
var ErrInvalidCharacter = errors.New("base58: invalid character")

// Decode converts a base58 string to its bytes. Leading '1's are the encoding
// of leading zero bytes and are restored as such, which is what makes the
// decoded length meaningful for format checks.
func Decode(s string) ([]byte, error) {
	if s == "" {
		return nil, nil
	}

	num := new(big.Int)
	radix := big.NewInt(58)
	for i := range len(s) {
		digit := index[s[i]]
		if digit < 0 {
			return nil, ErrInvalidCharacter
		}
		num.Mul(num, radix)
		num.Add(num, big.NewInt(int64(digit)))
	}

	decoded := num.Bytes()

	var zeros int
	for zeros < len(s) && s[zeros] == alphabet[0] {
		zeros++
	}

	out := make([]byte, zeros+len(decoded))
	copy(out[zeros:], decoded)
	return out, nil
}
