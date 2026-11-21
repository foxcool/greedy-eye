package postgres

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestNamedList: a refusal names at most maxNamedInRefusal items and counts
// the rest.
func TestNamedList(t *testing.T) {
	items := make([]string, maxNamedInRefusal+3)
	for i := range items {
		items[i] = fmt.Sprint(i)
	}
	got := namedList(items)
	assert.Contains(t, got, "; 9; and 3 more")
	assert.NotContains(t, got, "10")
	assert.Equal(t, "a; b", namedList([]string{"a", "b"}))
}

// TestConfigIDPattern: every spelling uuid.Parse accepts carries the pattern.
func TestConfigIDPattern(t *testing.T) {
	p := configIDPattern("0a1b2c3d-0000-4000-8000-00000000abcd")
	assert.Equal(t, "0a1b2c3d-?0000-?4000-?8000-?00000000abcd", p)
}
