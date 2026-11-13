package credentials

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/foxcool/greedy-eye/internal/entity"
	"github.com/foxcool/greedy-eye/internal/service/marketdata"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type namedMacroReader struct{ name string }

func (namedMacroReader) Latest(context.Context, string) (entity.MacroObservation, error) {
	return entity.MacroObservation{}, nil
}

func macroConfig(src *fakeSource) Config {
	return Config{
		Source: src,
		MacroReaders: map[string]MacroReaderFactory{
			"fred": func(a *entity.Account) (marketdata.MacroReader, error) {
				if a.Data["api_key"] == "" {
					return nil, errors.New("no key")
				}
				return namedMacroReader{name: a.ID}, nil
			},
		},
		KeylessMacroReaders: map[string]marketdata.MacroReader{"cbr": namedMacroReader{name: "keyless-cbr"}},
	}
}

func keyed(id string) *entity.Account {
	a := account(id, "fred", time.Now())
	a.Data["api_key"] = "k"
	return a
}

func TestMacroReadersForWithoutAFredAccountOffersOnlyKeyless(t *testing.T) {
	r := NewResolver(macroConfig(&fakeSource{}))

	got, err := r.MacroReadersFor(context.Background(), "u1")
	require.NoError(t, err)
	assert.Contains(t, got, "cbr")
	assert.NotContains(t, got, "fred")
}

func TestMacroReadersForBuildsFredFromTheAccountKey(t *testing.T) {
	r := NewResolver(macroConfig(&fakeSource{user: map[string][]*entity.Account{"u1": {keyed("fred-1")}}}))

	got, err := r.MacroReadersFor(context.Background(), "u1")
	require.NoError(t, err)
	assert.Equal(t, namedMacroReader{name: "fred-1"}, got["fred"])
	assert.Contains(t, got, "cbr")
}

func TestMacroReadersForSkipsAnAccountThatCannotBeBuilt(t *testing.T) {
	broken := account("fred-bad", "fred", time.Now())
	broken.Data["api_key"] = ""
	r := NewResolver(macroConfig(&fakeSource{user: map[string][]*entity.Account{"u1": {broken, keyed("fred-ok")}}}))

	got, err := r.MacroReadersFor(context.Background(), "u1")
	require.NoError(t, err)
	assert.Equal(t, namedMacroReader{name: "fred-ok"}, got["fred"])
}

// A macro key lives on a market_data account but is not a price source, so it
// must not be reported as an account lacking a price adapter.
func TestMacroAccountIsNotReportedAsMissingAPriceAdapter(t *testing.T) {
	r := NewResolver(macroConfig(&fakeSource{user: map[string][]*entity.Account{"u1": {keyed("fred-1")}}}))

	inv, err := r.PriceInventoryFor(context.Background(), "u1")
	require.NoError(t, err)
	assert.Empty(t, inv.Skipped)
}
