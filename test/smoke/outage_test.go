package smoke_test

import (
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// A smoke run asserts our wiring, not the uptime of a third-party feed, so a
// failure that is the provider's own must not turn main red. The marketdata
// handler reports provider failures as "<slug>: <adapter error>" strings, which
// is all the response carries, hence the pattern match. Only an upstream 5xx and
// a failed round trip (timeout, refused, reset, DNS) count: a 4xx, an empty
// response, a decode failure or a wrong source id are ours or the contract's and
// stay failures.
var providerOutage = regexp.MustCompile(`: unexpected status 5\d\d$|: request: `)

// providerOutageOnly reports whether errs is non-empty and made up entirely of
// provider outages for the given source. One unrelated error keeps the verdict
// false, so an outage cannot mask a real failure reported beside it.
func providerOutageOnly(source string, errs []string) bool {
	if len(errs) == 0 {
		return false
	}
	for _, e := range errs {
		if !strings.HasPrefix(e, source+": ") || !providerOutage.MatchString(e) {
			return false
		}
	}
	return true
}

func TestProviderOutageOnly(t *testing.T) {
	tests := []struct {
		name string
		errs []string
		want bool
	}{
		{"no errors", nil, false},
		{"upstream 502", []string{"cbr: cbr: unexpected status 502"}, true},
		{"upstream 500", []string{"cbr: cbr: unexpected status 500"}, true},
		{"transport timeout", []string{`cbr: cbr: request: Get "https://www.cbr.ru/x": context deadline exceeded`}, true},
		{"upstream 404 is not an outage", []string{"cbr: cbr: unexpected status 404"}, false},
		{"decode failure is not an outage", []string{"cbr: cbr: decode: EOF"}, false},
		{"other source is not ours to excuse", []string{"moex: moex: unexpected status 502"}, false},
		{"our own error", []string{"cbr: no base asset USD"}, false},
		{"outage beside a real failure", []string{"cbr: cbr: unexpected status 502", "storing prices: boom"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, providerOutageOnly("cbr", tt.errs))
		})
	}
}
