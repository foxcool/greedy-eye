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

// unkeyed is a fred account the factory refuses to build a reader from.
func unkeyed(id string) *entity.Account {
	a := account(id, "fred", time.Now())
	a.Data["api_key"] = ""
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
	r := NewResolver(macroConfig(&fakeSource{user: map[string][]*entity.Account{"u1": {unkeyed("fred-bad"), keyed("fred-ok")}}}))

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

func skippedIDs(inv Inventory) map[string]entity.SkipKind {
	out := map[string]entity.SkipKind{}
	for _, s := range inv.Skipped {
		out[s.AccountID] = s.Kind
	}
	return out
}

// The startup line must not call a publisher served because an account names
// it: with no key no reader is built, and GetMacroRates reports the series
// unavailable.
func TestInventoryDoesNotResolveMacroFromAnAccountThatCannotBeBuilt(t *testing.T) {
	broken := scoped(unkeyed("fred-bad"), entity.CapabilityMarketData)
	r := NewResolver(macroConfig(&fakeSource{system: []*entity.Account{broken}}))

	inv, err := r.Inventory(context.Background())
	require.NoError(t, err)

	assert.Equal(t, "macro=unresolved", roles(inv)["fred"])
	assert.Equal(t, "macro=keyless", roles(inv)["cbr"])
	require.Len(t, inv.Skipped, 1)
	assert.Equal(t, "fred-bad", inv.Skipped[0].AccountID)
	assert.Equal(t, entity.SkipCannotBuild, inv.Skipped[0].Kind)
	assert.Contains(t, inv.Skipped[0].Reason, "no key")
}

// A broken account ahead of a working one: the reader comes from the second,
// so the slug is served and only the first is named as passed over.
func TestInventoryReportsTheMacroAccountThatBuiltTheReader(t *testing.T) {
	r := NewResolver(macroConfig(&fakeSource{system: []*entity.Account{
		scoped(unkeyed("fred-bad"), entity.CapabilityMarketData),
		scoped(keyed("fred-ok"), entity.CapabilityMarketData),
	}}))

	inv, err := r.Inventory(context.Background())
	require.NoError(t, err)

	assert.Equal(t, "macro=system", roles(inv)["fred"])
	assert.Equal(t, map[string]entity.SkipKind{"fred-bad": entity.SkipCannotBuild}, skippedIDs(inv))
}

// A scoped account claims its slug even when it cannot be built, so the
// operator's own working account is never tried unattended. The line has to
// say what the readers say, not credit the account that would have worked.
func TestInventoryAgreesWithTheReadersWhenAScopedAccountShadowsTheOperators(t *testing.T) {
	r := NewResolver(macroConfig(&fakeSource{
		system: []*entity.Account{scoped(unkeyed("fred-bad"), entity.CapabilityMarketData)},
		user:   map[string][]*entity.Account{"op": {keyed("fred-ok")}},
	}))

	readers, err := r.MacroReadersFor(context.Background(), "")
	require.NoError(t, err)
	require.NotContains(t, readers, "fred")

	inv, err := r.Inventory(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "macro=unresolved", roles(inv)["fred"])
	assert.Equal(t, map[string]entity.SkipKind{"fred-bad": entity.SkipCannotBuild}, skippedIDs(inv))
}

// Where a publisher also has a keyless reader, a broken account leaves the
// slug on it rather than unresolved.
func TestInventoryFallsBackToTheKeylessMacroReader(t *testing.T) {
	cfg := macroConfig(&fakeSource{system: []*entity.Account{
		scoped(unkeyed("fred-bad"), entity.CapabilityMarketData),
	}})
	cfg.KeylessMacroReaders["fred"] = namedMacroReader{name: "keyless-fred"}

	inv, err := NewResolver(cfg).Inventory(context.Background())
	require.NoError(t, err)

	assert.Equal(t, "macro=keyless", roles(inv)["fred"])
	assert.Equal(t, map[string]entity.SkipKind{"fred-bad": entity.SkipCannotBuild}, skippedIDs(inv))
}
