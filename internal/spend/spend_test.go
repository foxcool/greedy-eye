package spend

import (
	"context"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
)

// A label that reaches the usage row must be one the store accepts, whatever
// arrived in X-Spend-Caller: one refused row would fail every retried flush.
func TestWithCallerKeepsLabelStorable(t *testing.T) {
	got := CallerFromContext(WithCaller(context.Background(), "sweep:prices"))
	assert.Equal(t, "sweep:prices", got)

	nested := CallerFromContext(WithCaller(WithCaller(context.Background(), "rpc:Sync"), "fetch"))
	assert.Equal(t, "rpc:Sync/fetch", nested)

	bad := CallerFromContext(WithCaller(context.Background(), "a\xffb"))
	assert.True(t, utf8.ValidString(bad))

	long := CallerFromContext(WithCaller(context.Background(), strings.Repeat("€", 3000)))
	assert.LessOrEqual(t, len(long), maxCallerLen)
	assert.True(t, utf8.ValidString(long))
}
