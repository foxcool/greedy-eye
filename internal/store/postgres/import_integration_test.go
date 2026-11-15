//go:build integration

package postgres

import (
	"context"
	"errors"
	"log/slog"
	"testing"

	"connectrpc.com/connect"
	apiv1 "github.com/foxcool/greedy-eye/api/v1"
	"github.com/foxcool/greedy-eye/internal/entity"
	"github.com/foxcool/greedy-eye/internal/entity/entitytest"
	"github.com/foxcool/greedy-eye/internal/middleware"
	"github.com/foxcool/greedy-eye/internal/service/marketdata"
	"github.com/foxcool/greedy-eye/internal/service/portfolio"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var errInjectedWrite = errors.New("injected holding write failure")

// failingWriteStore fails the failOn-th holding write, counted across writes
// made on the store directly and inside InHoldingsTx; the writes before it run
// real SQL.
type failingWriteStore struct {
	*PortfolioStore
	failOn, writes int
}

func (s *failingWriteStore) next() error {
	s.writes++
	if s.writes == s.failOn {
		return errInjectedWrite
	}
	return nil
}

func (s *failingWriteStore) CreateHolding(ctx context.Context, h *entity.Holding) (*entity.Holding, error) {
	if err := s.next(); err != nil {
		return nil, err
	}
	return s.PortfolioStore.CreateHolding(ctx, h)
}

func (s *failingWriteStore) UpdateHolding(ctx context.Context, h *entity.Holding, fields []string) (*entity.Holding, error) {
	if err := s.next(); err != nil {
		return nil, err
	}
	return s.PortfolioStore.UpdateHolding(ctx, h, fields)
}

func (s *failingWriteStore) InHoldingsTx(ctx context.Context, fn func(portfolio.HoldingWriter) error) error {
	return s.PortfolioStore.InHoldingsTx(ctx, func(w portfolio.HoldingWriter) error {
		return fn(failingWriter{HoldingWriter: w, s: s})
	})
}

type failingWriter struct {
	portfolio.HoldingWriter
	s *failingWriteStore
}

func (w failingWriter) CreateHolding(ctx context.Context, h *entity.Holding) (*entity.Holding, error) {
	if err := w.s.next(); err != nil {
		return nil, err
	}
	return w.HoldingWriter.CreateHolding(ctx, h)
}

func (w failingWriter) UpdateHolding(ctx context.Context, h *entity.Holding, fields []string) (*entity.Holding, error) {
	if err := w.s.next(); err != nil {
		return nil, err
	}
	return w.HoldingWriter.UpdateHolding(ctx, h, fields)
}

// TestImportPositionsCommitsAllOrNone: a paper fill is one batch that debits
// the quote and credits the base. When the base write fails after the quote
// was written, the quote must read as it did before the batch.
func TestImportPositionsCommitsAllOrNone(t *testing.T) {
	pool := getTestPool(t)
	ctx := context.Background()
	s := NewPortfolioStore(pool)
	assets := NewMarketDataStore(pool)
	user := createTestUser(t, NewUserStore(pool))
	quote := createTestAsset(t, assets, "Import Quote")
	base := createTestAsset(t, assets, "Import Base")

	account, err := s.CreateAccount(ctx, &entity.Account{UserID: user.ID, Name: "paper", Type: entity.AccountTypeManual})
	require.NoError(t, err)
	quoteHolding, err := s.CreateHolding(ctx, entitytest.Holding(entity.Holding{
		AssetID: quote.ID, AccountID: account.ID,
		Amount: decimal.NewFromInt(10000), Decimals: 2,
	}, entitytest.HoldingState{Source: entity.SourceManual}))
	require.NoError(t, err)

	md := marketdata.NewHandler(assets, slog.New(slog.DiscardHandler))
	batch := func(st portfolio.Store) *apiv1.ImportPositionsResponse {
		decimals := uint32(2)
		resp, err := portfolio.NewHandler(st, slog.New(slog.DiscardHandler)).WithMarketDataClient(md).
			ImportPositions(middleware.ContextWithUser(ctx, user), connect.NewRequest(&apiv1.ImportPositionsRequest{
				AccountId: account.ID,
				Positions: []*apiv1.ImportPositionItem{
					{AssetId: &quote.ID, Amount: "40", Decimals: &decimals}, // debit: 100.00 -> 40.00
					{AssetId: &base.ID, Amount: "3", Decimals: &decimals},   // credit: new holding
				},
			}))
		require.NoError(t, err)
		return resp.Msg
	}
	holdings := func() map[string]string {
		rows, _, err := s.ListHoldings(ctx, portfolio.ListHoldingsOpts{AccountID: account.ID, PageSize: 100})
		require.NoError(t, err)
		out := map[string]string{}
		for _, h := range rows {
			out[h.AssetID] = h.Amount.String()
		}
		return out
	}

	t.Run("the base write fails: the quote is unchanged", func(t *testing.T) {
		resp := batch(&failingWriteStore{PortfolioStore: s, failOn: 2})

		assert.Equal(t, int32(2), resp.Failed, "%v", resp.Items)
		got, err := s.GetHolding(ctx, quoteHolding.ID)
		require.NoError(t, err)
		assert.Equal(t, "10000", got.Amount.String(), "the debit rolled back with the batch")
		assert.Equal(t, map[string]string{quote.ID: "10000"}, holdings(), "no base holding was created")
	})

	t.Run("a clean batch lands whole", func(t *testing.T) {
		resp := batch(s)

		assert.Zero(t, resp.Failed, "%v", resp.Items)
		assert.Equal(t, map[string]string{quote.ID: "4000", base.ID: "300"}, holdings())
	})
}
