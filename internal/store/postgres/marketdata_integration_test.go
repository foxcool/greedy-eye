//go:build integration

package postgres

import (
	"context"
	"math/rand/v2"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/foxcool/greedy-eye/internal/entity"
	"github.com/foxcool/greedy-eye/internal/service/marketdata"
	"github.com/foxcool/greedy-eye/internal/store"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func createTestAsset(t *testing.T, s *MarketDataStore, name string) *entity.Asset {
	t.Helper()
	symbol := strings.ToUpper(regexp.MustCompile(`\s`).ReplaceAllString(name, ""))
	asset := &entity.Asset{
		Symbol: symbol,
		Name:   name,
		Type:   entity.AssetTypeCryptocurrency,
		Tags:   []string{name, "test"},
	}
	created, err := s.CreateAsset(context.Background(), asset)
	require.NoError(t, err, "asset creation failed")
	require.NotNil(t, created)
	assert.Equal(t, asset.Symbol, created.Symbol)
	assert.Equal(t, asset.Name, created.Name)
	assert.Equal(t, asset.Type, created.Type)
	assert.ElementsMatch(t, asset.Tags, created.Tags)
	assert.NotEmpty(t, created.ID)
	assert.NotZero(t, created.CreatedAt)

	return created
}

func createTestPrice(t *testing.T, s *MarketDataStore, assetID, baseAssetID, sourceID string) *entity.StoredPrice {
	t.Helper()
	nd := func() decimal.NullDecimal {
		return decimal.NullDecimal{Decimal: decimal.NewFromInt(rand.Int64N(1000000)), Valid: true}
	}

	price := &entity.StoredPrice{
		SourceID:    sourceID,
		AssetID:     assetID,
		BaseAssetID: baseAssetID,
		Interval:    "latest",
		Decimals:    4,
		Last:        decimal.NewFromInt(rand.Int64N(1000000)),
		Open:        nd(),
		Close:       nd(),
		High:        nd(),
		Low:         nd(),
		Volume:      nd(),
		Timestamp:   time.Now(),
	}

	created, err := s.CreatePrice(context.Background(), price)
	require.NoError(t, err)
	assert.NotEmpty(t, created.ID)
	assert.Equal(t, price.SourceID, created.SourceID)
	assert.Equal(t, price.AssetID, created.AssetID)
	assert.Equal(t, price.BaseAssetID, created.BaseAssetID)

	return created
}

func TestCreateAsset(t *testing.T) {
	pool := getTestPool(t)
	s := NewMarketDataStore(pool)

	t.Run("Valid asset creation", func(t *testing.T) {
		asset := &entity.Asset{
			Symbol: "BTC",
			Name:   "Bitcoin",
			Type:   entity.AssetTypeCryptocurrency,
			Tags:   []string{"crypto", "pow"},
		}
		created, err := s.CreateAsset(context.Background(), asset)
		require.NoError(t, err)
		assert.NotEmpty(t, created.ID)
		assert.Equal(t, asset.Symbol, created.Symbol)
		assert.Equal(t, asset.Name, created.Name)
		assert.Equal(t, asset.Type, created.Type)
		assert.ElementsMatch(t, asset.Tags, created.Tags)
	})

	t.Run("Missing required fields (name)", func(t *testing.T) {
		asset := &entity.Asset{
			Symbol: "NONAME",
			Type:   entity.AssetTypeCryptocurrency,
		}
		_, err := s.CreateAsset(context.Background(), asset)
		assert.Error(t, err)
		assert.ErrorIs(t, err, store.ErrInvalidArgument)
	})

	t.Run("Missing required fields (type)", func(t *testing.T) {
		asset := &entity.Asset{
			Symbol: "NOTYPE",
			Name:   "No Type Asset",
		}
		_, err := s.CreateAsset(context.Background(), asset)
		assert.Error(t, err)
		assert.ErrorIs(t, err, store.ErrInvalidArgument)
	})

	t.Run("nil asset", func(t *testing.T) {
		_, err := s.CreateAsset(context.Background(), nil)
		assert.Error(t, err)
		assert.ErrorIs(t, err, store.ErrInvalidArgument)
	})
}

func TestAssetIdentity(t *testing.T) {
	pool := getTestPool(t)
	s := NewMarketDataStore(pool)
	ctx := context.Background()

	t.Run("crypto market defaults to crypto", func(t *testing.T) {
		created, err := s.CreateAsset(ctx, &entity.Asset{
			Symbol: "IDDEFAULT",
			Name:   "Identity Default",
			Type:   entity.AssetTypeCryptocurrency,
		})
		require.NoError(t, err)
		assert.Equal(t, entity.MarketCrypto, created.Market)
	})

	t.Run("market is normalized", func(t *testing.T) {
		created, err := s.CreateAsset(ctx, &entity.Asset{
			Symbol: "IDNORM",
			Name:   "Identity Normalized",
			Type:   entity.AssetTypeStock,
			Market: " NASDAQ ",
		})
		require.NoError(t, err)
		assert.Equal(t, "nasdaq", created.Market)
	})

	t.Run("stock without market is rejected", func(t *testing.T) {
		_, err := s.CreateAsset(ctx, &entity.Asset{
			Symbol: "IDNOMARKET",
			Name:   "Identity No Market",
			Type:   entity.AssetTypeStock,
		})
		assert.ErrorIs(t, err, store.ErrInvalidArgument)
	})

	t.Run("duplicate (symbol, market, type) is rejected", func(t *testing.T) {
		asset := &entity.Asset{
			Symbol: "IDDUP",
			Name:   "Identity Dup",
			Type:   entity.AssetTypeCryptocurrency,
		}
		_, err := s.CreateAsset(ctx, asset)
		require.NoError(t, err)
		_, err = s.CreateAsset(ctx, &entity.Asset{
			Symbol: "IDDUP",
			Name:   "Identity Dup Again",
			Type:   entity.AssetTypeCryptocurrency,
		})
		assert.ErrorIs(t, err, store.ErrConstraint)
	})

	t.Run("same symbol on another market coexists and makes symbol lookup ambiguous", func(t *testing.T) {
		_, err := s.CreateAsset(ctx, &entity.Asset{
			Symbol: "IDAAPL",
			Name:   "Identity Apple Token",
			Type:   entity.AssetTypeCryptocurrency,
		})
		require.NoError(t, err)

		// Unique symbol resolves.
		found, err := s.GetAssetBySymbol(ctx, "idaapl")
		require.NoError(t, err)
		assert.Equal(t, entity.MarketCrypto, found.Market)

		_, err = s.CreateAsset(ctx, &entity.Asset{
			Symbol: "IDAAPL",
			Name:   "Identity Apple Stock",
			Type:   entity.AssetTypeStock,
			Market: "nasdaq",
			Quote:  "USD",
		})
		require.NoError(t, err)

		// Ambiguous symbol fails explicitly instead of silently picking one.
		_, err = s.GetAssetBySymbol(ctx, "IDAAPL")
		assert.ErrorIs(t, err, store.ErrInvalidArgument)
	})

	t.Run("update market and quote", func(t *testing.T) {
		created, err := s.CreateAsset(ctx, &entity.Asset{
			Symbol: "IDUPD",
			Name:   "Identity Update",
			Type:   entity.AssetTypeCryptocurrency,
		})
		require.NoError(t, err)

		created.Market = "MOEX"
		created.Quote = "RUB"
		updated, err := s.UpdateAsset(ctx, created, []string{"market", "quote"})
		require.NoError(t, err)
		assert.Equal(t, "moex", updated.Market)
		assert.Equal(t, "RUB", updated.Quote)
	})
}

func TestGetAsset(t *testing.T) {
	pool := getTestPool(t)
	s := NewMarketDataStore(pool)
	asset := createTestAsset(t, s, "TestGetAsset")

	t.Run("Get existing asset by ID", func(t *testing.T) {
		res, err := s.GetAsset(context.Background(), asset.ID)
		require.NoError(t, err)
		assert.Equal(t, asset.ID, res.ID)
		assert.Equal(t, asset.Symbol, res.Symbol)
		assert.Equal(t, asset.Name, res.Name)
		assert.Equal(t, asset.Type, res.Type)
		assert.ElementsMatch(t, asset.Tags, res.Tags)
	})

	t.Run("Get non-existent asset by ID", func(t *testing.T) {
		_, err := s.GetAsset(context.Background(), uuid.New().String())
		assert.Error(t, err)
		assert.ErrorIs(t, err, store.ErrNotFound)
	})

	t.Run("Invalid asset ID", func(t *testing.T) {
		_, err := s.GetAsset(context.Background(), "not-a-uuid")
		assert.Error(t, err)
		assert.ErrorIs(t, err, store.ErrInvalidArgument)
	})

	t.Run("Empty asset ID", func(t *testing.T) {
		_, err := s.GetAsset(context.Background(), "")
		assert.Error(t, err)
		assert.ErrorIs(t, err, store.ErrInvalidArgument)
	})
}

func TestUpdateAsset(t *testing.T) {
	pool := getTestPool(t)
	s := NewMarketDataStore(pool)
	asset := createTestAsset(t, s, "TestUpdateAsset")

	t.Run("Update asset name and tags", func(t *testing.T) {
		updated := &entity.Asset{
			ID:   asset.ID,
			Name: "New Name",
			Tags: []string{"crypto", "pos"},
		}
		res, err := s.UpdateAsset(context.Background(), updated, []string{"name", "tags"})
		require.NoError(t, err)
		assert.Equal(t, asset.ID, res.ID)
		assert.Equal(t, "New Name", res.Name)
		assert.ElementsMatch(t, []string{"crypto", "pos"}, res.Tags)
		assert.Equal(t, asset.Symbol, res.Symbol)
		assert.Equal(t, asset.Type, res.Type)
	})

	t.Run("Update type", func(t *testing.T) {
		updated := &entity.Asset{
			ID:   asset.ID,
			Type: entity.AssetTypeStock,
		}
		res, err := s.UpdateAsset(context.Background(), updated, []string{"type"})
		require.NoError(t, err)
		assert.Equal(t, entity.AssetTypeStock, res.Type)
	})

	t.Run("Update non-existent asset", func(t *testing.T) {
		updated := &entity.Asset{
			ID:   uuid.New().String(),
			Name: "Doesn't Exist",
		}
		_, err := s.UpdateAsset(context.Background(), updated, []string{"name"})
		assert.Error(t, err)
		assert.ErrorIs(t, err, store.ErrNotFound)
	})
}

func TestDeleteAsset(t *testing.T) {
	pool := getTestPool(t)
	s := NewMarketDataStore(pool)
	asset := createTestAsset(t, s, "TestDeleteAsset")

	t.Run("Delete existing asset", func(t *testing.T) {
		err := s.DeleteAsset(context.Background(), asset.ID)
		assert.NoError(t, err)
		// Verify deletion
		_, err = s.GetAsset(context.Background(), asset.ID)
		assert.ErrorIs(t, err, store.ErrNotFound)
	})

	t.Run("Delete non-existent asset", func(t *testing.T) {
		err := s.DeleteAsset(context.Background(), uuid.New().String())
		assert.ErrorIs(t, err, store.ErrNotFound)
	})

	t.Run("Invalid asset ID", func(t *testing.T) {
		err := s.DeleteAsset(context.Background(), "not-a-uuid")
		assert.ErrorIs(t, err, store.ErrInvalidArgument)
	})
}

func TestListAssets(t *testing.T) {
	pool := getTestPool(t)
	s := NewMarketDataStore(pool)

	// Create test assets
	assets := make(map[string]*entity.Asset)
	for _, name := range []string{"ListAsset1", "ListAsset2", "ListAsset3"} {
		assets[name] = createTestAsset(t, s, name)
	}

	t.Run("List all", func(t *testing.T) {
		res, _, err := s.ListAssets(context.Background(), marketdata.ListAssetsOpts{})
		require.NoError(t, err)
		assert.GreaterOrEqual(t, len(res), 3)
	})

	t.Run("Filter by tag", func(t *testing.T) {
		res, _, err := s.ListAssets(context.Background(), marketdata.ListAssetsOpts{
			Tags: []string{"ListAsset2"},
		})
		require.NoError(t, err)
		assert.Len(t, res, 1)
		assert.Contains(t, res[0].Tags, "ListAsset2")
	})

	t.Run("Pagination", func(t *testing.T) {
		res, nextToken, err := s.ListAssets(context.Background(), marketdata.ListAssetsOpts{
			PageSize: 2,
		})
		require.NoError(t, err)
		assert.Len(t, res, 2)
		assert.NotEmpty(t, nextToken)

		// Get next page
		res2, _, err := s.ListAssets(context.Background(), marketdata.ListAssetsOpts{
			PageSize:  2,
			PageToken: nextToken,
		})
		require.NoError(t, err)
		assert.NotEmpty(t, res2)
	})
}

func TestCreatePrice(t *testing.T) {
	pool := getTestPool(t)
	s := NewMarketDataStore(pool)
	asset1 := createTestAsset(t, s, "PriceAsset1")
	asset2 := createTestAsset(t, s, "PriceAsset2")

	t.Run("Create price", func(t *testing.T) {
		price := &entity.StoredPrice{
			SourceID:    "binance",
			AssetID:     asset1.ID,
			BaseAssetID: asset2.ID,
			Interval:    "1m",
			Last:        decimal.NewFromInt(1000000),
			Decimals:    2,
			Timestamp:   time.Now(),
		}
		created, err := s.CreatePrice(context.Background(), price)
		require.NoError(t, err)
		assert.NotEmpty(t, created.ID)
		assert.Equal(t, price.SourceID, created.SourceID)
	})

	t.Run("Missing asset_id", func(t *testing.T) {
		price := &entity.StoredPrice{
			SourceID:    "binance",
			BaseAssetID: asset2.ID,
			Interval:    "1m",
			Last:        decimal.NewFromInt(1000000),
		}
		_, err := s.CreatePrice(context.Background(), price)
		assert.ErrorIs(t, err, store.ErrInvalidArgument)
	})

	t.Run("Non-existent asset_id", func(t *testing.T) {
		price := &entity.StoredPrice{
			SourceID:    "binance",
			AssetID:     uuid.New().String(),
			BaseAssetID: asset2.ID,
			Interval:    "1m",
			Last:        decimal.NewFromInt(1000000),
		}
		_, err := s.CreatePrice(context.Background(), price)
		assert.ErrorIs(t, err, store.ErrConstraint)
	})
}

// TestPriceProvenanceRoundTrips: the column exists so a quote nobody traded can
// be told apart from one somebody paid for. A round trip that silently returned
// "traded" for a row the source said nothing about would put the claim in our
// mouth rather than the source's, which is the failure the NULL is there to
// prevent.
func TestPriceProvenanceRoundTrips(t *testing.T) {
	pool := getTestPool(t)
	s := NewMarketDataStore(pool)
	asset := createTestAsset(t, s, "ProvenanceAsset")
	base := createTestAsset(t, s, "ProvenanceBase")

	write := func(t *testing.T, p entity.PriceProvenance, at time.Time) {
		t.Helper()
		_, err := s.CreatePrice(context.Background(), &entity.StoredPrice{
			SourceID:    "moex",
			AssetID:     asset.ID,
			BaseAssetID: base.ID,
			Interval:    "latest",
			Last:        decimal.NewFromInt(9355000000),
			Decimals:    8,
			Timestamp:   at,
			Provenance:  p,
		})
		require.NoError(t, err)
	}

	start := time.Now()
	write(t, entity.PriceProvenanceUnknown, start)
	write(t, entity.PriceProvenanceTraded, start.Add(time.Second))
	write(t, entity.PriceProvenanceAppraised, start.Add(2*time.Second))

	latest, err := s.GetLatestPrice(context.Background(), asset.ID, base.ID, "moex")
	require.NoError(t, err)
	assert.Equal(t, entity.PriceProvenanceAppraised, latest.Provenance)

	history, _, err := s.ListPriceHistory(context.Background(), marketdata.ListPriceHistoryOpts{
		AssetID:     asset.ID,
		BaseAssetID: base.ID,
	})
	require.NoError(t, err)
	require.Len(t, history, 3)
	assert.Equal(t, entity.PriceProvenanceUnknown, history[0].Provenance,
		"a source that said nothing must not read back as a claim")
	assert.Equal(t, entity.PriceProvenanceTraded, history[1].Provenance)
	assert.Equal(t, entity.PriceProvenanceAppraised, history[2].Provenance)
}

func TestGetLatestPrice(t *testing.T) {
	pool := getTestPool(t)
	s := NewMarketDataStore(pool)
	asset1 := createTestAsset(t, s, "LatestPriceAsset1")
	asset2 := createTestAsset(t, s, "LatestPriceAsset2")

	createTestPrice(t, s, asset1.ID, asset2.ID, "exchange1")
	time.Sleep(10 * time.Millisecond) // Ensure different timestamps
	price2 := createTestPrice(t, s, asset1.ID, asset2.ID, "exchange2")
	time.Sleep(10 * time.Millisecond)
	price3 := createTestPrice(t, s, asset1.ID, asset2.ID, "aggregator")

	t.Run("Get latest price (any source)", func(t *testing.T) {
		res, err := s.GetLatestPrice(context.Background(), asset1.ID, asset2.ID, "")
		require.NoError(t, err)
		assert.Equal(t, price3.ID, res.ID)
	})

	t.Run("Get latest price with specific source", func(t *testing.T) {
		res, err := s.GetLatestPrice(context.Background(), asset1.ID, asset2.ID, "exchange2")
		require.NoError(t, err)
		assert.Equal(t, price2.ID, res.ID)
	})

	t.Run("Get latest price with non-existing source", func(t *testing.T) {
		_, err := s.GetLatestPrice(context.Background(), asset1.ID, asset2.ID, "unknown")
		assert.ErrorIs(t, err, store.ErrNotFound)
	})

	t.Run("Get latest price with non-existing asset", func(t *testing.T) {
		_, err := s.GetLatestPrice(context.Background(), uuid.New().String(), asset2.ID, "")
		assert.ErrorIs(t, err, store.ErrNotFound)
	})
}

func TestListPriceHistory(t *testing.T) {
	pool := getTestPool(t)
	s := NewMarketDataStore(pool)
	asset := createTestAsset(t, s, "HistoryAsset")
	baseAsset := createTestAsset(t, s, "HistoryBaseAsset")

	// Create prices with small delays to ensure ordering
	for i := 0; i < 5; i++ {
		createTestPrice(t, s, asset.ID, baseAsset.ID, "exchange")
		time.Sleep(10 * time.Millisecond)
	}

	t.Run("List price history", func(t *testing.T) {
		from := time.Now().Add(-time.Minute)
		to := time.Now()
		res, _, err := s.ListPriceHistory(context.Background(), marketdata.ListPriceHistoryOpts{
			AssetID:     asset.ID,
			BaseAssetID: baseAsset.ID,
			From:        &from,
			To:          &to,
		})
		require.NoError(t, err)
		assert.GreaterOrEqual(t, len(res), 5)
	})

	t.Run("List with non-existing asset", func(t *testing.T) {
		res, _, err := s.ListPriceHistory(context.Background(), marketdata.ListPriceHistoryOpts{
			AssetID:     uuid.New().String(),
			BaseAssetID: baseAsset.ID,
		})
		require.NoError(t, err)
		assert.Empty(t, res)
	})

	t.Run("Pagination", func(t *testing.T) {
		res, nextToken, err := s.ListPriceHistory(context.Background(), marketdata.ListPriceHistoryOpts{
			AssetID:     asset.ID,
			BaseAssetID: baseAsset.ID,
			PageSize:    2,
		})
		require.NoError(t, err)
		assert.Len(t, res, 2)
		assert.NotEmpty(t, nextToken)

		// Get next page
		res2, _, err := s.ListPriceHistory(context.Background(), marketdata.ListPriceHistoryOpts{
			AssetID:     asset.ID,
			BaseAssetID: baseAsset.ID,
			PageSize:    2,
			PageToken:   nextToken,
		})
		require.NoError(t, err)
		assert.NotEmpty(t, res2)
	})
}

func TestDeletePrice(t *testing.T) {
	pool := getTestPool(t)
	s := NewMarketDataStore(pool)
	asset := createTestAsset(t, s, "DeletePriceAsset1")
	baseAsset := createTestAsset(t, s, "DeletePriceAsset2")
	price := createTestPrice(t, s, asset.ID, baseAsset.ID, "exchange")

	t.Run("Delete price", func(t *testing.T) {
		err := s.DeletePrice(context.Background(), price.ID)
		assert.NoError(t, err)

		// Verify deletion
		_, err = s.GetLatestPrice(context.Background(), asset.ID, baseAsset.ID, "")
		assert.ErrorIs(t, err, store.ErrNotFound)
	})

	t.Run("Delete non-existing price", func(t *testing.T) {
		err := s.DeletePrice(context.Background(), uuid.New().String())
		assert.ErrorIs(t, err, store.ErrNotFound)
	})

	t.Run("Delete with invalid ID", func(t *testing.T) {
		err := s.DeletePrice(context.Background(), "invalid-id")
		assert.ErrorIs(t, err, store.ErrInvalidArgument)
	})
}

func TestDeletePrices(t *testing.T) {
	pool := getTestPool(t)
	s := NewMarketDataStore(pool)
	asset := createTestAsset(t, s, "DeletePricesAsset1")
	baseAsset := createTestAsset(t, s, "DeletePricesAsset2")

	for i := 0; i < 3; i++ {
		createTestPrice(t, s, asset.ID, baseAsset.ID, "exchange")
	}

	t.Run("Delete prices by asset", func(t *testing.T) {
		err := s.DeletePrices(context.Background(), marketdata.DeletePricesOpts{
			AssetID:     asset.ID,
			BaseAssetID: baseAsset.ID,
		})
		assert.NoError(t, err)

		// Verify deletion
		_, err = s.GetLatestPrice(context.Background(), asset.ID, baseAsset.ID, "")
		assert.ErrorIs(t, err, store.ErrNotFound)
	})

	t.Run("Delete non-existing prices", func(t *testing.T) {
		err := s.DeletePrices(context.Background(), marketdata.DeletePricesOpts{
			AssetID:     uuid.New().String(),
			BaseAssetID: baseAsset.ID,
		})
		assert.ErrorIs(t, err, store.ErrNotFound)
	})
}

func TestFindAssetByIdentity(t *testing.T) {
	pool := getTestPool(t)
	s := NewMarketDataStore(pool)
	ctx := context.Background()

	created, err := s.CreateAsset(ctx, &entity.Asset{
		Symbol: "identbtc",
		Name:   "Identity BTC",
		Type:   entity.AssetTypeCryptocurrency,
	})
	require.NoError(t, err)

	got, err := s.FindAssetByIdentity(ctx, " identbtc ", "CRYPTO", entity.AssetTypeCryptocurrency)
	require.NoError(t, err)
	assert.Equal(t, created.ID, got.ID)

	_, err = s.FindAssetByIdentity(ctx, "IDENTBTC", "nasdaq", entity.AssetTypeCryptocurrency)
	require.ErrorIs(t, err, store.ErrNotFound)

	_, err = s.FindAssetByIdentity(ctx, "IDENTBTC", "crypto", entity.AssetTypeUnspecified)
	require.ErrorIs(t, err, store.ErrInvalidArgument)
}

// assetIDs is a readability helper for asserting on selection order.
func assetIDs(assets []*entity.Asset) []string {
	ids := make([]string, 0, len(assets))
	for _, a := range assets {
		ids = append(ids, a.ID)
	}
	return ids
}

// TestListStalePricingTargets covers the selection an unattended sweep runs on:
// never-attempted assets first, freshness scoped to one source, the per-sweep
// limit, symbol tiers and the quarantine exclusion.
func TestListStalePricingTargets(t *testing.T) {
	pool := getTestPool(t)
	s := NewMarketDataStore(pool)
	ctx := context.Background()
	now := time.Now()

	fresh := createTestAsset(t, s, "StaleFresh")
	overdue := createTestAsset(t, s, "StaleOverdue")
	never := createTestAsset(t, s, "StaleNever")

	require.NoError(t, s.RecordPriceAttempts(ctx, marketdata.RecordAttemptsOpts{
		SourceID: "coingecko",
		At:       now.Add(-10 * time.Minute),
		Priced:   []string{fresh.ID},
		TTL:      time.Hour,
	}))
	require.NoError(t, s.RecordPriceAttempts(ctx, marketdata.RecordAttemptsOpts{
		SourceID: "coingecko",
		At:       now.Add(-3 * time.Hour),
		Priced:   []string{overdue.ID},
		TTL:      time.Hour,
	}))

	t.Run("never attempted comes first, fresh is skipped", func(t *testing.T) {
		got, err := s.ListStalePricingTargets(ctx, marketdata.StalePricingOpts{
			SourceID: "coingecko",
			Now:      now,
		})
		require.NoError(t, err)
		require.Len(t, got, 2)
		assert.Equal(t, never.ID, got[0].ID, "never-attempted asset must lead the rotation")
		assert.Equal(t, overdue.ID, got[1].ID)
	})

	t.Run("freshness is per source", func(t *testing.T) {
		got, err := s.ListStalePricingTargets(ctx, marketdata.StalePricingOpts{
			SourceID: "binance",
			Now:      now,
		})
		require.NoError(t, err)
		assert.ElementsMatch(t, []string{fresh.ID, overdue.ID, never.ID}, assetIDs(got))
	})

	t.Run("limit keeps the oldest", func(t *testing.T) {
		got, err := s.ListStalePricingTargets(ctx, marketdata.StalePricingOpts{
			SourceID: "coingecko",
			Now:      now,
			Limit:    1,
		})
		require.NoError(t, err)
		require.Len(t, got, 1)
		assert.Equal(t, never.ID, got[0].ID)
	})

	t.Run("symbol tiers partition the catalogue", func(t *testing.T) {
		exempt := []string{never.Symbol}

		got, err := s.ListStalePricingTargets(ctx, marketdata.StalePricingOpts{
			SourceID: "coingecko",
			Now:      now,
			Symbols:  exempt,
		})
		require.NoError(t, err)
		assert.Equal(t, []string{never.ID}, assetIDs(got))

		got, err = s.ListStalePricingTargets(ctx, marketdata.StalePricingOpts{
			SourceID:       "coingecko",
			Now:            now,
			ExcludeSymbols: exempt,
		})
		require.NoError(t, err)
		assert.Equal(t, []string{overdue.ID}, assetIDs(got))
	})

	t.Run("quarantined assets are excluded", func(t *testing.T) {
		_, err := s.SetAssetVerdict(ctx, never.ID, "scam", nil, nil, "heuristic")
		require.NoError(t, err)

		got, err := s.ListStalePricingTargets(ctx, marketdata.StalePricingOpts{
			SourceID:        "coingecko",
			Now:             now,
			ExcludeVerdicts: []string{"scam", "impersonation"},
		})
		require.NoError(t, err)
		assert.Equal(t, []string{overdue.ID}, assetIDs(got))
	})

	t.Run("source is required", func(t *testing.T) {
		_, err := s.ListStalePricingTargets(ctx, marketdata.StalePricingOpts{Now: now})
		require.ErrorIs(t, err, store.ErrInvalidArgument)
	})
}

// TestRecordPriceAttempts_MissBackoff verifies consecutive misses push the next
// attempt out exponentially up to the cap, and that a hit resets it — this is
// what keeps an asset the provider never lists from blocking the rotation.
func TestRecordPriceAttempts_MissBackoff(t *testing.T) {
	pool := getTestPool(t)
	s := NewMarketDataStore(pool)
	ctx := context.Background()
	base := time.Now().Truncate(time.Second)

	asset := createTestAsset(t, s, "BackoffAsset")

	nextAttempt := func() (time.Time, int) {
		t.Helper()
		var next time.Time
		var misses int
		require.NoError(t, pool.QueryRow(ctx,
			`SELECT next_attempt_at, misses FROM price_fetch_attempts
			 WHERE asset_id = $1 AND source_id = 'coingecko'`, asset.ID).Scan(&next, &misses))
		return next, misses
	}

	miss := func(at time.Time) {
		t.Helper()
		require.NoError(t, s.RecordPriceAttempts(ctx, marketdata.RecordAttemptsOpts{
			SourceID:   "coingecko",
			At:         at,
			Missed:     []string{asset.ID},
			TTL:        time.Hour,
			MaxBackoff: 4 * time.Hour,
		}))
	}

	miss(base)
	next, misses := nextAttempt()
	assert.Equal(t, 1, misses)
	assert.WithinDuration(t, base.Add(time.Hour), next, time.Second)

	miss(base)
	next, misses = nextAttempt()
	assert.Equal(t, 2, misses, "consecutive misses accumulate")
	assert.WithinDuration(t, base.Add(2*time.Hour), next, time.Second)

	miss(base)
	next, _ = nextAttempt()
	assert.WithinDuration(t, base.Add(4*time.Hour), next, time.Second)

	miss(base)
	next, misses = nextAttempt()
	assert.Equal(t, 4, misses)
	assert.WithinDuration(t, base.Add(4*time.Hour), next, time.Second, "backoff is capped")

	require.NoError(t, s.RecordPriceAttempts(ctx, marketdata.RecordAttemptsOpts{
		SourceID: "coingecko",
		At:       base,
		Priced:   []string{asset.ID},
		TTL:      time.Hour,
	}))
	next, misses = nextAttempt()
	assert.Zero(t, misses, "a hit resets the miss counter")
	assert.WithinDuration(t, base.Add(time.Hour), next, time.Second)

	t.Run("no attempts is a no-op", func(t *testing.T) {
		require.NoError(t, s.RecordPriceAttempts(ctx, marketdata.RecordAttemptsOpts{SourceID: "coingecko"}))
	})
}

// TestResetPriceAttempts: the accrued back-off is the schedule's whole memory,
// so forgiving it has to move both the counter and the deadline. It must also
// stay inside the source it names — one provider coming back says nothing about
// another, and a reset that leaked across sources would spend every provider's
// quota re-asking a catalogue nobody claimed was wrong.
func TestResetPriceAttempts(t *testing.T) {
	pool := getTestPool(t)
	s := NewMarketDataStore(pool)
	ctx := context.Background()
	base := time.Now().Truncate(time.Second)

	asset := createTestAsset(t, s, "ResetAsset")

	read := func(source string) (time.Time, int) {
		t.Helper()
		var next time.Time
		var misses int
		require.NoError(t, pool.QueryRow(ctx,
			`SELECT next_attempt_at, misses FROM price_fetch_attempts
			 WHERE asset_id = $1 AND source_id = $2`, asset.ID, source).Scan(&next, &misses))
		return next, misses
	}

	// Drive both sources to the cap, so the reset has something to forgive and
	// a neighbour that must survive it untouched.
	for _, source := range []string{"coingecko", "moex"} {
		for range 4 {
			require.NoError(t, s.RecordPriceAttempts(ctx, marketdata.RecordAttemptsOpts{
				SourceID: source, At: base, Missed: []string{asset.ID},
				TTL: time.Hour, MaxBackoff: 4 * time.Hour,
			}))
		}
	}
	_, misses := read("coingecko")
	require.Equal(t, 4, misses, "precondition: back-off has accrued")

	at := base.Add(time.Minute)
	freed, err := s.ResetPriceAttempts(ctx, "coingecko", at)
	require.NoError(t, err)
	assert.Equal(t, int64(1), freed, "reports how many assets it freed")

	next, misses := read("coingecko")
	assert.Zero(t, misses, "the miss counter is forgiven")
	assert.WithinDuration(t, at, next, time.Second,
		"and the deadline moves with it: zeroing the counter alone would leave the "+
			"asset deferred until a date computed from history just disclaimed")

	next, misses = read("moex")
	assert.Equal(t, 4, misses, "a reset does not cross into another source")
	assert.WithinDuration(t, base.Add(4*time.Hour), next, time.Second)

	t.Run("the attempt record itself survives", func(t *testing.T) {
		var attemptedAt time.Time
		require.NoError(t, pool.QueryRow(ctx,
			`SELECT attempted_at FROM price_fetch_attempts
			 WHERE asset_id = $1 AND source_id = 'coingecko'`, asset.ID).Scan(&attemptedAt))
		assert.WithinDuration(t, base, attemptedAt, time.Second,
			"what was asked and when is a fact; only the conclusion is withdrawn")
	})

	t.Run("a clear schedule frees nothing", func(t *testing.T) {
		freed, err := s.ResetPriceAttempts(ctx, "coingecko", at)
		require.NoError(t, err)
		assert.Zero(t, freed, "zero is a real answer: the schedule was not the problem")
	})

	t.Run("source is required", func(t *testing.T) {
		_, err := s.ResetPriceAttempts(ctx, "", at)
		require.ErrorIs(t, err, store.ErrInvalidArgument)
	})
}

// TestCreatePrices_TwoSourcesSameInstant: two providers may record the same
// asset at the same moment. The old UNIQUE(asset_id, timestamp) index made that
// a constraint violation, which the sweep then reported as a failed fetch.
func TestCreatePrices_TwoSourcesSameInstant(t *testing.T) {
	pool := getTestPool(t)
	s := NewMarketDataStore(pool)
	ctx := context.Background()

	asset := createTestAsset(t, s, "TwoSourceAsset")
	base := createTestAsset(t, s, "TwoSourceBase")
	ts := time.Now()

	price := func(source string) *entity.StoredPrice {
		return &entity.StoredPrice{
			SourceID:    source,
			AssetID:     asset.ID,
			BaseAssetID: base.ID,
			Interval:    "latest",
			Decimals:    8,
			Last:        decimal.NewFromInt(42),
			Timestamp:   ts,
		}
	}

	count, err := s.CreatePrices(ctx, []*entity.StoredPrice{price("coingecko"), price("binance")})
	require.NoError(t, err)
	assert.Equal(t, 2, count)
}

// TestListAssets_IDsFilter: the explicit reconciliation path reads exactly the
// assets it names instead of paging the catalogue and filtering in Go.
func TestListAssets_IDsFilter(t *testing.T) {
	pool := getTestPool(t)
	s := NewMarketDataStore(pool)
	ctx := context.Background()

	want := createTestAsset(t, s, "IDsFilterWanted")
	createTestAsset(t, s, "IDsFilterOther")

	got, _, err := s.ListAssets(ctx, marketdata.ListAssetsOpts{IDs: []string{want.ID}})
	require.NoError(t, err)
	assert.Equal(t, []string{want.ID}, assetIDs(got))
}

// TestListAssetExternalRefs: the pricing path needs the reverse of
// FindAssetIDByExternalRef — which chain a contract lives on — for every asset
// in a sweep, in one round trip.
func TestListAssetExternalRefs(t *testing.T) {
	pool := getTestPool(t)
	s := NewMarketDataStore(pool)
	ctx := context.Background()

	onEth := createTestAsset(t, s, "RefsOnEth")
	onBase := createTestAsset(t, s, "RefsOnBase")
	bare := createTestAsset(t, s, "RefsNone")

	for _, ref := range []*entity.AssetExternalRef{
		{AssetID: onEth.ID, Source: entity.OnchainSource("eth"), Ref: "0xaaa"},
		{AssetID: onEth.ID, Source: "coingecko", Ref: "some-coin"},
		{AssetID: onBase.ID, Source: entity.OnchainSource("base"), Ref: "0xbbb"},
	} {
		_, err := s.CreateAssetExternalRef(ctx, ref)
		require.NoError(t, err)
	}

	got, err := s.ListAssetExternalRefs(ctx, []string{onEth.ID, onBase.ID, bare.ID})
	require.NoError(t, err)
	require.Len(t, got, 3, "an asset with no refs simply contributes none")

	chains := map[string]string{}
	for _, ref := range got {
		if chain, ok := entity.ChainFromOnchainSource(ref.Source); ok {
			chains[ref.AssetID] = chain
		}
	}
	assert.Equal(t, map[string]string{onEth.ID: "eth", onBase.ID: "base"}, chains)

	t.Run("no assets is no query", func(t *testing.T) {
		rows, err := s.ListAssetExternalRefs(ctx, nil)
		require.NoError(t, err)
		assert.Empty(t, rows)
	})
}

// seedHoldingForAsset gives an asset a position to be counted in, so a test can
// ask whether something took it out of the sums.
func seedHoldingForAsset(t *testing.T, pool *pgxpool.Pool, assetID string) string {
	t.Helper()
	ctx := context.Background()
	user := createTestUser(t, NewUserStore(pool))
	portfolios := NewPortfolioStore(pool)

	account, err := portfolios.CreateAccount(ctx, &entity.Account{
		UserID:       user.ID,
		Name:         "risk flag test",
		Type:         entity.AccountTypeManual,
		Capabilities: []entity.AccountCapability{entity.CapabilityManualPositions},
	})
	require.NoError(t, err)

	holding, err := portfolios.CreateHolding(ctx, &entity.Holding{
		AssetID:   assetID,
		AccountID: account.ID,
		Decimals:  8,
		Source:    entity.SourceManual,
	})
	require.NoError(t, err)
	return holding.ID
}

func holdingExcluded(t *testing.T, pool *pgxpool.Pool, holdingID string) bool {
	t.Helper()
	var excluded bool
	require.NoError(t, pool.QueryRow(context.Background(),
		`SELECT excluded FROM holdings WHERE id = $1`, holdingID).Scan(&excluded))
	return excluded
}

// TestAssetRiskFlags covers axis 2 end to end in the store: flags accumulate,
// come back newest first, belong to exactly one asset, and cannot be written
// without a review date.
func TestAssetRiskFlags(t *testing.T) {
	pool := getTestPool(t)
	s := NewMarketDataStore(pool)
	ctx := context.Background()

	asset := createTestAsset(t, s, "RiskFlagged")
	other := createTestAsset(t, s, "RiskUnflagged")
	review := time.Now().Add(30 * 24 * time.Hour).UTC()

	first, err := s.CreateAssetRiskFlag(ctx, &entity.AssetRiskFlag{
		AssetID: asset.ID, Kind: "exploit", Note: "bridge drained",
		ActionHint: "exit_soon", ReviewAt: &review, SetBy: "user:tester",
	})
	require.NoError(t, err)
	assert.NotEmpty(t, first.ID)

	// A second event is a second row: collapsing them would erase the first
	// one's history the moment the second is filed.
	second, err := s.CreateAssetRiskFlag(ctx, &entity.AssetRiskFlag{
		AssetID: asset.ID, Kind: "depeg", ReviewAt: &review,
	})
	require.NoError(t, err)

	got, err := s.ListAssetRiskFlags(ctx, asset.ID)
	require.NoError(t, err)
	require.Len(t, got, 2)
	assert.Equal(t, second.ID, got[0].ID, "newest first: the oldest flag is the least likely to still describe the situation")
	assert.Equal(t, "exploit", got[1].Kind)
	assert.Equal(t, "exit_soon", got[1].ActionHint)
	assert.Equal(t, "bridge drained", got[1].Note)
	require.NotNil(t, got[1].ReviewAt)
	assert.WithinDuration(t, review, *got[1].ReviewAt, time.Second)
	// An absent note is empty, not a scan error on NULL.
	assert.Empty(t, got[0].Note)

	empty, err := s.ListAssetRiskFlags(ctx, other.ID)
	require.NoError(t, err)
	assert.Empty(t, empty, "flags belong to one asset")

	t.Run("review date is required", func(t *testing.T) {
		_, err := s.CreateAssetRiskFlag(ctx, &entity.AssetRiskFlag{AssetID: asset.ID, Kind: "delisting"})
		require.ErrorIs(t, err, store.ErrInvalidArgument)
	})

	t.Run("delete refuses another asset's flag", func(t *testing.T) {
		err := s.DeleteAssetRiskFlag(ctx, other.ID, first.ID)
		require.ErrorIs(t, err, store.ErrNotFound)

		still, err := s.ListAssetRiskFlags(ctx, asset.ID)
		require.NoError(t, err)
		assert.Len(t, still, 2)
	})

	t.Run("delete removes exactly one", func(t *testing.T) {
		require.NoError(t, s.DeleteAssetRiskFlag(ctx, asset.ID, first.ID))
		left, err := s.ListAssetRiskFlags(ctx, asset.ID)
		require.NoError(t, err)
		require.Len(t, left, 1)
		assert.Equal(t, second.ID, left[0].ID)
	})
}

// TestAssetRiskFlagDoesNotExcludeHolding is the invariant of axis 2 and the
// regression this whole separation exists to prevent: a risk flag marks real
// money, so it must not do what an identity verdict does and take the position
// out of the sums. The check is on holdings.excluded because that is the switch
// a verdict flips — if a future change ever wires flags into it, this fails.
func TestAssetRiskFlagDoesNotExcludeHolding(t *testing.T) {
	pool := getTestPool(t)
	s := NewMarketDataStore(pool)
	ctx := context.Background()

	asset := createTestAsset(t, s, "RiskFlagKeepsCounting")
	holdingID := seedHoldingForAsset(t, pool, asset.ID)

	excludedBefore := holdingExcluded(t, pool, holdingID)
	require.False(t, excludedBefore, "a plain holding starts counted")

	review := time.Now().Add(24 * time.Hour).UTC()
	_, err := s.CreateAssetRiskFlag(ctx, &entity.AssetRiskFlag{
		AssetID: asset.ID, Kind: "sanctions_freeze", ActionHint: "hold", ReviewAt: &review,
	})
	require.NoError(t, err)

	assert.False(t, holdingExcluded(t, pool, holdingID),
		"a risk flag must never exclude a holding: the asset is real and so is its value")
}

// TestFindTickerIncumbent covers the shape personal-go65 is about: 1.89M "USDT"
// off a contract that is not Tether's, on the chain where Tether's is known.
// Every negative case here is a real one from the dev catalogue, where a naive
// "two assets share a symbol" rule would have quarantined live positions.
func TestFindTickerIncumbent(t *testing.T) {
	pool := getTestPool(t)
	s := NewMarketDataStore(pool)
	ctx := context.Background()

	usd := createTestAsset(t, s, "IncumbentBase")

	// mint creates one asset with a symbol and market of its own choosing;
	// identity is (symbol, market, type), so same-ticker assets need distinct
	// markets — exactly how the catalogue stores an isolated contract.
	mint := func(symbol, name, market string) *entity.Asset {
		a, err := s.CreateAsset(ctx, &entity.Asset{
			Symbol: symbol, Name: name, Market: market, Type: entity.AssetTypeCryptocurrency,
		})
		require.NoError(t, err)
		return a
	}
	bind := func(assetID, chain, contract string) {
		_, err := s.CreateAssetExternalRef(ctx, &entity.AssetExternalRef{
			AssetID: assetID, Source: entity.OnchainSource(chain), Ref: contract,
		})
		require.NoError(t, err)
	}

	tether := mint("TCOL", "Tether", "crypto")
	bind(tether.ID, "eth", "0xdac17f958d2ee523a2206206994597c13d831ec7")
	bind(tether.ID, "bsc", "0x55d398326f99059ff775485246999027b3197955")
	createTestPrice(t, s, tether.ID, usd.ID, "coingecko")

	impostor := mint("TCOL", "TCOL", "onchain:eth/0x7f1ffe636a11d92f31b2874b574cff2a565569a8")
	bind(impostor.ID, "eth", "0x7f1ffe636a11d92f31b2874b574cff2a565569a8")

	t.Run("newcomer on a held chain is judged", func(t *testing.T) {
		got, err := s.FindTickerIncumbent(ctx, impostor.ID)
		require.NoError(t, err)
		assert.Equal(t, tether.ID, got)
	})

	t.Run("the incumbent is not judged by its own impostor", func(t *testing.T) {
		_, err := s.FindTickerIncumbent(ctx, tether.ID)
		assert.ErrorIs(t, err, store.ErrNotFound, "seniority has to be asymmetric or both sides condemn each other")
	})

	t.Run("same asset on another chain is not a collision", func(t *testing.T) {
		// Tether's own bsc contract: same ticker, different chain, one asset.
		multichain := mint("MCOL", "Multichain", "crypto")
		bind(multichain.ID, "eth", "0xaaa1")
		bind(multichain.ID, "polygon", "0xbbb1")
		createTestPrice(t, s, multichain.ID, usd.ID, "coingecko")

		other := mint("MCOL", "Multichain on arbitrum", "onchain:arbitrum/0xccc1")
		bind(other.ID, "arbitrum", "0xccc1")

		_, err := s.FindTickerIncumbent(ctx, other.ID)
		assert.ErrorIs(t, err, store.ErrNotFound)
	})

	t.Run("an unlisted incumbent condemns nobody", func(t *testing.T) {
		// The catalogue's ordinary duplicate: a legacy row and its on-chain twin,
		// neither of which any provider prices. Judging on symbol alone would
		// quarantine a live position (AETHUSDC, PDEX, PF on dev).
		legacy := mint("UCOL", "Unlisted", "crypto")
		bind(legacy.ID, "eth", "0xddd1")

		twin := mint("UCOL", "Unlisted", "onchain:eth/0xeee1")
		bind(twin.ID, "eth", "0xeee1")

		_, err := s.FindTickerIncumbent(ctx, twin.ID)
		assert.ErrorIs(t, err, store.ErrNotFound)
	})

	t.Run("an asset with no chain of its own is never a claimant", func(t *testing.T) {
		bare := mint("TCOL", "Bare", "manual")
		_, err := s.FindTickerIncumbent(ctx, bare.ID)
		assert.ErrorIs(t, err, store.ErrNotFound)
	})

	t.Run("rejects a malformed id", func(t *testing.T) {
		_, err := s.FindTickerIncumbent(ctx, "not-a-uuid")
		assert.ErrorIs(t, err, store.ErrInvalidArgument)
	})
}

// TestGetAssetBySymbol_ContractMarketDoesNotShadowATicker: a contract is minted
// by whoever pays the gas, so it must not contend for a ticker that a named
// market already carries. Dev 2026-08-04: two junk tokens calling themselves USD
// arrived with a whale wallet and every valuation on the instance started
// failing on "symbol USD is ambiguous", which reads as $0 on screen.
func TestGetAssetBySymbol_ContractMarketDoesNotShadowATicker(t *testing.T) {
	pool := getTestPool(t)
	s := NewMarketDataStore(pool)
	ctx := context.Background()

	mint := func(symbol, name, market string, typ entity.AssetType) *entity.Asset {
		a, err := s.CreateAsset(ctx, &entity.Asset{Symbol: symbol, Name: name, Market: market, Type: typ})
		require.NoError(t, err)
		return a
	}

	quote := mint("SHDW", "Shadow Dollar", "forex", entity.AssetTypeForex)
	mint("SHDW", "Shadow Shitcoin", "onchain:base/0x306fb9107924a5e1ce254ef4522f6085d903e784", entity.AssetTypeCryptocurrency)
	mint("SHDW", "Shadow Tether", "onchain:bsc/0x037499ebb453c6c84f1888c783ef8b75a257bd29", entity.AssetTypeCryptocurrency)

	got, err := s.GetAssetBySymbol(ctx, "shdw")
	require.NoError(t, err, "two minted lookalikes must not take the ticker offline")
	assert.Equal(t, quote.ID, got.ID)

	t.Run("a symbol only a contract carries still resolves", func(t *testing.T) {
		only := mint("ONLYC", "Only Contract", "onchain:eth/0xabc123", entity.AssetTypeCryptocurrency)
		got, err := s.GetAssetBySymbol(ctx, "ONLYC")
		require.NoError(t, err)
		assert.Equal(t, only.ID, got.ID)
	})

	t.Run("two contracts on one ticker are still ambiguous", func(t *testing.T) {
		mint("TWOC", "Two Contracts A", "onchain:eth/0xaaa111", entity.AssetTypeCryptocurrency)
		mint("TWOC", "Two Contracts B", "onchain:bsc/0xbbb222", entity.AssetTypeCryptocurrency)
		_, err := s.GetAssetBySymbol(ctx, "TWOC")
		assert.ErrorIs(t, err, store.ErrInvalidArgument)
	})

	t.Run("two named markets are still ambiguous", func(t *testing.T) {
		mint("DUAL", "Dual on nasdaq", "nasdaq", entity.AssetTypeStock)
		mint("DUAL", "Dual on moex", "moex", entity.AssetTypeStock)
		_, err := s.GetAssetBySymbol(ctx, "DUAL")
		assert.ErrorIs(t, err, store.ErrInvalidArgument)
	})
}

func TestDeleteAssetExternalRef(t *testing.T) {
	pool := getTestPool(t)
	s := NewMarketDataStore(pool)
	ctx := context.Background()

	real := createTestAsset(t, s, "Aave Token")
	other := createTestAsset(t, s, "Other Token")

	bind := func(asset *entity.Asset, source, ref string) *entity.AssetExternalRef {
		t.Helper()
		created, err := s.CreateAssetExternalRef(ctx, &entity.AssetExternalRef{
			AssetID: asset.ID, Source: source, Ref: ref, Origin: entity.RefOriginAuto,
		})
		require.NoError(t, err)
		return created
	}

	eth := bind(real, "onchain:ethereum", "0xreal")
	poly := bind(real, "onchain:polygon", "0xfake")

	t.Run("a ref of another asset is not this asset's to remove", func(t *testing.T) {
		// The asset id is part of the WHERE clause, so a mistyped ref id cannot
		// detach a binding from an unrelated asset.
		err := s.DeleteAssetExternalRef(ctx, other.ID, poly.ID)
		require.ErrorIs(t, err, store.ErrNotFound)

		refs, err := s.ListAssetExternalRefs(ctx, []string{real.ID})
		require.NoError(t, err)
		assert.Len(t, refs, 2, "nothing was removed")
	})

	t.Run("removing the wrong binding leaves the right one", func(t *testing.T) {
		require.NoError(t, s.DeleteAssetExternalRef(ctx, real.ID, poly.ID))

		refs, err := s.ListAssetExternalRefs(ctx, []string{real.ID})
		require.NoError(t, err)
		require.Len(t, refs, 1)
		assert.Equal(t, eth.ID, refs[0].ID)

		// And the contract is free to be resolved on its own merits again.
		_, err = s.FindAssetIDByExternalRef(ctx, "onchain:polygon", "0xfake")
		require.ErrorIs(t, err, store.ErrNotFound)
	})

	t.Run("removing it twice is not found, not a silent success", func(t *testing.T) {
		err := s.DeleteAssetExternalRef(ctx, real.ID, poly.ID)
		require.ErrorIs(t, err, store.ErrNotFound)
	})

	t.Run("bad input is refused before the query", func(t *testing.T) {
		require.ErrorIs(t, s.DeleteAssetExternalRef(ctx, "not-a-uuid", eth.ID), store.ErrInvalidArgument)
		require.ErrorIs(t, s.DeleteAssetExternalRef(ctx, real.ID, "not-a-uuid"), store.ErrInvalidArgument)
	})

	t.Run("the contract can be rebound after the mistake is undone", func(t *testing.T) {
		// The repair has to leave the namespace usable: UNIQUE(source, ref)
		// would otherwise keep the slot occupied by a row that no longer exists.
		rebound := bind(other, "onchain:polygon", "0xfake")
		assert.Equal(t, other.ID, rebound.AssetID)
	})
}

// TestPricingStatus covers the read side of the attempt log: whether ANY source
// ever priced an asset, over what period it has been asked about and by how
// many sources. The aggregate is the point — attempts are per (asset, source),
// so "nothing priced this" is a claim about all of them at once.
func TestPricingStatus(t *testing.T) {
	pool := getTestPool(t)
	s := NewMarketDataStore(pool)
	ctx := context.Background()
	now := time.Now().Truncate(time.Millisecond)

	silent := createTestAsset(t, s, "StatusSilent")   // asked by two sources, never priced
	priced := createTestAsset(t, s, "StatusPriced")   // one source priced it once
	unasked := createTestAsset(t, s, "StatusUnasked") // no attempt record at all

	first := now.Add(-11 * 24 * time.Hour)
	require.NoError(t, s.RecordPriceAttempts(ctx, marketdata.RecordAttemptsOpts{
		SourceID: "moex", At: first, Missed: []string{silent.ID}, TTL: time.Hour,
	}))
	require.NoError(t, s.RecordPriceAttempts(ctx, marketdata.RecordAttemptsOpts{
		SourceID: "coingecko", At: now, Missed: []string{silent.ID}, TTL: time.Hour,
	}))
	require.NoError(t, s.RecordPriceAttempts(ctx, marketdata.RecordAttemptsOpts{
		SourceID: "coingecko", At: now, Priced: []string{priced.ID}, TTL: time.Hour,
	}))

	got, err := s.PricingStatus(ctx, []string{silent.ID, priced.ID, unasked.ID})
	require.NoError(t, err)

	byAsset := map[string]*entity.AssetPricingStatus{}
	for _, st := range got {
		byAsset[st.AssetID] = st
	}

	require.Contains(t, byAsset, silent.ID)
	assert.False(t, byAsset[silent.ID].EverPriced)
	assert.Equal(t, uint32(2), byAsset[silent.ID].SourcesAsked)
	assert.WithinDuration(t, first, byAsset[silent.ID].FirstAskedAt, time.Second,
		"the silence is dated from the first source that asked, not the last")
	assert.WithinDuration(t, now, byAsset[silent.ID].LastAskedAt, time.Second)

	require.Contains(t, byAsset, priced.ID)
	assert.True(t, byAsset[priced.ID].EverPriced)

	assert.NotContains(t, byAsset, unasked.ID,
		"an asset with no record is omitted: an empty status would read as 'asked, nothing came back'")

	t.Run("no ids is not a query", func(t *testing.T) {
		got, err := s.PricingStatus(ctx, nil)
		require.NoError(t, err)
		assert.Empty(t, got)
	})
}

// TestSweepSchedule covers the reading that tells a frozen queue from an idle
// one: an instance whose whole catalogue is backed off produces exactly the
// same empty selection as one that is fully current, and until this existed the
// only way to tell them apart was a psql session on the host.
//
// The source id is unique to this test because the pool is shared: counting
// another test's attempt rows would make the assertions depend on run order.
func TestSweepSchedule(t *testing.T) {
	pool := getTestPool(t)
	s := NewMarketDataStore(pool)
	ctx := context.Background()
	now := time.Now()
	source := "sched-" + uuid.NewString()[:8]

	due := createTestAsset(t, s, "SchedDue")
	deferred := createTestAsset(t, s, "SchedDeferred")
	createTestAsset(t, s, "SchedNever")

	// Priced three hours ago on a one-hour TTL: due again.
	require.NoError(t, s.RecordPriceAttempts(ctx, marketdata.RecordAttemptsOpts{
		SourceID: source,
		At:       now.Add(-3 * time.Hour),
		Priced:   []string{due.ID},
		TTL:      time.Hour,
	}))
	// Missed, so pushed out past now: deferred.
	require.NoError(t, s.RecordPriceAttempts(ctx, marketdata.RecordAttemptsOpts{
		SourceID:   source,
		At:         now,
		Missed:     []string{deferred.ID},
		TTL:        time.Hour,
		MaxBackoff: 7 * 24 * time.Hour,
	}))

	t.Run("due, deferred and never-attempted are counted apart", func(t *testing.T) {
		got, err := s.SweepSchedule(ctx, marketdata.SweepScheduleOpts{
			SourceIDs: []string{source},
			Now:       now,
		})
		require.NoError(t, err)
		require.Len(t, got, 1)

		sched := got[0]
		assert.Equal(t, source, sched.SourceID)
		assert.Equal(t, uint32(1), sched.DueNow)
		assert.Equal(t, uint32(1), sched.Deferred)
		// Every asset in the catalogue is never-attempted for a source id that
		// only this test uses, minus the two it just recorded against.
		assert.Greater(t, sched.NeverAttempted, uint32(0))
		assert.False(t, sched.SoonestDue.IsZero(), "a deferred asset must date the queue")
		assert.True(t, sched.SoonestDue.After(now))
		assert.Equal(t, uint32(1), sched.MaxMisses)
	})

	t.Run("nothing deferred leaves the timestamps absent", func(t *testing.T) {
		// Far enough in the future that the deferred row has come due too.
		got, err := s.SweepSchedule(ctx, marketdata.SweepScheduleOpts{
			SourceIDs: []string{source},
			Now:       now.Add(30 * 24 * time.Hour),
		})
		require.NoError(t, err)
		require.Len(t, got, 1)

		assert.Equal(t, uint32(0), got[0].Deferred)
		assert.True(t, got[0].SoonestDue.IsZero(),
			"a zero time is how a caller recognises 'nothing is deferred'; a date would read as a fact")
		assert.True(t, got[0].LatestDeferred.IsZero())
	})

	t.Run("a source never asked still gets a row", func(t *testing.T) {
		// The most important thing this can report: silence from a source that
		// was never asked must not be omitted, or it reads as absence of trouble.
		unasked := "sched-" + uuid.NewString()[:8]
		got, err := s.SweepSchedule(ctx, marketdata.SweepScheduleOpts{
			SourceIDs: []string{unasked},
			Now:       now,
		})
		require.NoError(t, err)
		require.Len(t, got, 1)
		assert.Equal(t, unasked, got[0].SourceID)
		assert.Equal(t, uint32(0), got[0].DueNow)
		assert.Greater(t, got[0].NeverAttempted, uint32(0))
	})

	t.Run("quarantined assets are not counted as work", func(t *testing.T) {
		scam := createTestAsset(t, s, "SchedScam")
		_, err := s.SetAssetVerdict(ctx, scam.ID, "scam", nil, nil, "curated")
		require.NoError(t, err)

		unasked := "sched-" + uuid.NewString()[:8]
		withScam, err := s.SweepSchedule(ctx, marketdata.SweepScheduleOpts{
			SourceIDs: []string{unasked},
			Now:       now,
		})
		require.NoError(t, err)
		require.Len(t, withScam, 1)

		filtered, err := s.SweepSchedule(ctx, marketdata.SweepScheduleOpts{
			SourceIDs:       []string{unasked},
			Now:             now,
			ExcludeVerdicts: []string{"scam", "impersonation"},
		})
		require.NoError(t, err)
		require.Len(t, filtered, 1)

		assert.Less(t, filtered[0].NeverAttempted, withScam[0].NeverAttempted,
			"the sweep never asks about quarantined assets, so counting them would report work nobody intends to do")
	})
}
