package marketdata

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"connectrpc.com/connect"
	apiv1 "github.com/foxcool/greedy-eye/api/v1"
	"github.com/foxcool/greedy-eye/internal/entity"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// countingReader answers one fixed print and counts how often it is asked.
type countingReader struct {
	calls int
	obs   entity.MacroObservation
	err   error
}

func (r *countingReader) Latest(_ context.Context, _ string) (entity.MacroObservation, error) {
	r.calls++
	return r.obs, r.err
}

type fakeMacroSource map[string]MacroReader

func (f fakeMacroSource) MacroReadersFor(context.Context, string) (map[string]MacroReader, error) {
	return f, nil
}

func obsOn(date, value string) entity.MacroObservation {
	d, err := time.Parse(time.DateOnly, date)
	if err != nil {
		panic(err)
	}
	return entity.MacroObservation{Value: decimal.RequireFromString(value), Date: d}
}

func macroHandler(src MacroSource) *Handler {
	return NewHandler(nil, slog.Default()).WithMacroSource(src)
}

func bySeries(resp *apiv1.GetMacroRatesResponse) map[string]*apiv1.MacroSeries {
	out := map[string]*apiv1.MacroSeries{}
	for _, s := range resp.Series {
		out[s.Series] = s
	}
	return out
}

func TestGetMacroRatesSecondCallWithinTTLMakesNoUpstreamCall(t *testing.T) {
	fred := &countingReader{obs: obsOn("2026-11-12", "3.88")}
	cbr := &countingReader{obs: obsOn("2026-11-13", "14")}
	h := macroHandler(fakeMacroSource{"fred": fred, "cbr": cbr})

	for i := 0; i < 2; i++ {
		_, err := h.GetMacroRates(context.Background(), connect.NewRequest(&apiv1.GetMacroRatesRequest{}))
		require.NoError(t, err)
	}

	assert.Equal(t, 1, fred.calls, "the repeat must be served from memory")
	assert.Equal(t, 1, cbr.calls)
}

func TestGetMacroRatesRereadsAfterTTL(t *testing.T) {
	cbr := &countingReader{obs: obsOn("2026-11-13", "14")}
	h := macroHandler(fakeMacroSource{"cbr": cbr})
	now := time.Date(2026, 11, 13, 9, 0, 0, 0, time.UTC)
	h.macro.now = func() time.Time { return now }

	req := connect.NewRequest(&apiv1.GetMacroRatesRequest{Series: []string{"cbr_key_rate"}})
	_, err := h.GetMacroRates(context.Background(), req)
	require.NoError(t, err)

	now = now.Add(macroTTL - time.Minute)
	_, err = h.GetMacroRates(context.Background(), req)
	require.NoError(t, err)
	assert.Equal(t, 1, cbr.calls)

	now = now.Add(2 * time.Minute)
	_, err = h.GetMacroRates(context.Background(), req)
	require.NoError(t, err)
	assert.Equal(t, 2, cbr.calls, "a read older than the TTL must go back upstream")
}

func TestGetMacroRatesWithoutFredKeyKeepsCBRSeries(t *testing.T) {
	// The resolver offers no "fred" reader when no account carries a key.
	cbr := &countingReader{obs: obsOn("2026-11-13", "14")}
	h := macroHandler(fakeMacroSource{"cbr": cbr})

	resp, err := h.GetMacroRates(context.Background(), connect.NewRequest(&apiv1.GetMacroRatesRequest{}))
	require.NoError(t, err)

	got := bySeries(resp.Msg)
	fed := got["fed_funds"]
	require.NotNil(t, fed)
	assert.False(t, fed.Available)
	assert.NotEmpty(t, fed.GetUnavailableReason())
	assert.Empty(t, fed.Value)

	key := got["cbr_key_rate"]
	require.NotNil(t, key)
	assert.True(t, key.Available)
	assert.Equal(t, "14", key.Value)
	assert.Equal(t, "cbr", key.Source)
}

func TestGetMacroRatesCarriesSourceDateDistinctFromReadTime(t *testing.T) {
	h := macroHandler(fakeMacroSource{"fred": &countingReader{obs: obsOn("2026-11-12", "3.88")}})
	now := time.Date(2026, 11, 13, 9, 30, 0, 0, time.UTC)
	h.macro.now = func() time.Time { return now }

	resp, err := h.GetMacroRates(context.Background(), connect.NewRequest(&apiv1.GetMacroRatesRequest{Series: []string{"fed_funds"}}))
	require.NoError(t, err)

	s := bySeries(resp.Msg)["fed_funds"]
	assert.Equal(t, "2026-11-12", s.ObservationDate)
	assert.True(t, s.GetReadAt().AsTime().Equal(now))
	assert.NotEqual(t, s.ObservationDate, s.GetReadAt().AsTime().Format(time.DateOnly))
}

func TestGetMacroRatesUpstreamFailureIsPerSeriesAndNotCached(t *testing.T) {
	fred := &countingReader{err: errors.New("fred: status 500")}
	cbr := &countingReader{obs: obsOn("2026-11-13", "14")}
	h := macroHandler(fakeMacroSource{"fred": fred, "cbr": cbr})

	for i := 0; i < 2; i++ {
		resp, err := h.GetMacroRates(context.Background(), connect.NewRequest(&apiv1.GetMacroRatesRequest{}))
		require.NoError(t, err)
		got := bySeries(resp.Msg)
		assert.False(t, got["fed_funds"].Available)
		assert.NotEmpty(t, got["fed_funds"].GetUnavailableReason())
		assert.True(t, got["cbr_key_rate"].Available)
	}
	assert.Equal(t, 2, fred.calls, "a failure must not be remembered as an answer")
	assert.Equal(t, 1, cbr.calls)
}

func TestGetMacroRatesRejectsUnknownSeries(t *testing.T) {
	h := macroHandler(fakeMacroSource{})

	_, err := h.GetMacroRates(context.Background(), connect.NewRequest(&apiv1.GetMacroRatesRequest{Series: []string{"nope"}}))
	require.Error(t, err)
	assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
}
