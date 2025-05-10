//go:build integration

package storage

import (
	"testing"
	"time"

	"github.com/foxcool/greedy-eye/internal/api/models"
	"github.com/foxcool/greedy-eye/internal/services/storage/ent"
	"github.com/foxcool/greedy-eye/internal/services/storage/ent/account"
	"github.com/foxcool/greedy-eye/internal/services/storage/ent/asset"
	"github.com/foxcool/greedy-eye/internal/services/storage/ent/transaction"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
)

func TestEntUserToProtoUser(t *testing.T) {
	t.Run("Convert correct ent user", func(t *testing.T) {
		entUser := &ent.User{
			UUID:        uuid.New(),
			Email:       "user@example.com",
			Name:        "Test User",
			Preferences: map[string]string{"currency": "USD"},
		}
		protoUser, err := entUserToProtoUser(entUser)
		if !assert.NoError(t, err) && !assert.NotNil(t, protoUser) {
			assert.FailNow(t, "conversion failed")
		}
		assert.Equal(t, entUser.UUID.String(), protoUser.Id)
		assert.Equal(t, entUser.Email, protoUser.Email)
		assert.Equal(t, entUser.Name, protoUser.Name)
		assert.NotNil(t, protoUser.CreatedAt)
		assert.NotNil(t, protoUser.UpdatedAt)
		assert.Equal(t, entUser.Preferences, protoUser.Preferences)
	})
}

func TestEntAssetToProtoAsset(t *testing.T) {
	t.Run("Convert correct ent asset", func(t *testing.T) {
		entAsset := &ent.Asset{
			UUID:   uuid.New(),
			Symbol: "BTC",
			Name:   "Bitcoin",
			Type:   asset.TypeCryptocurrency,
			Tags:   []string{"crypto", "store_of_value"},
		}
		protoAsset, err := entAssetToProtoAsset(entAsset)
		if !assert.NoError(t, err) && !assert.NotNil(t, protoAsset) {
			assert.FailNow(t, "conversion failed")
		}
		assert.Equal(t, entAsset.UUID.String(), protoAsset.Id)
		assert.Equal(t, entAsset.Name, protoAsset.Name)
		assert.Equal(t, entAsset.Symbol, *protoAsset.Symbol)
		assert.ElementsMatch(t, entAsset.Tags, protoAsset.Tags)
		assert.NotNil(t, protoAsset.CreatedAt)
		assert.NotNil(t, protoAsset.UpdatedAt)
	})
}

func TestEntAccountToProtoAccount(t *testing.T) {
	storageService := getTransactionedService(t)
	user := createTestUser(t, storageService, "TestUser", "test@example.com")
	userUUID, err := stringToUUID(user.Id)
	if !assert.NoError(t, err) {
		assert.FailNow(t, "Failed to convert user ID to UUID")
	}

	t.Run("Convert correct ent account", func(t *testing.T) {
		entAccount := &ent.Account{
			UUID:        uuid.New(),
			Name:        "Spot",
			Description: "My wallet",
			Type:        account.TypeWallet,
			Data:        map[string]string{"address": "abc"},
			CreatedAt:   time.Unix(12, 0),
			UpdatedAt:   time.Unix(13, 0),
			Edges: ent.AccountEdges{
				User: &ent.User{
					UUID: userUUID,
				},
			},
		}
		account, err := entAccountToProtoAccount(entAccount)
		if !assert.NoError(t, err) || !assert.NotNil(t, account) {
			assert.FailNow(t, "Failed to convert ent account to proto account")
		}
		assert.Equal(t, entAccount.UUID.String(), account.Id)
		assert.Equal(t, user.Id, account.UserId)
		assert.Equal(t, entAccount.Name, account.Name)
		assert.Equal(t, &entAccount.Description, account.Description)
		assert.Equal(t, models.AccountType_ACCOUNT_TYPE_WALLET, account.Type)
	})
}

func TestEntHoldingToProtoHolding(t *testing.T) {
	storageService := getTransactionedService(t)
	user := createTestUser(t, storageService, "TestUser", "test@example.com")
	account := createTestAccount(t, storageService, user.Id, "TestAccount", models.AccountType_ACCOUNT_TYPE_BROKER)
	accountUUID, err := stringToUUID(account.Id)
	if !assert.NoError(t, err) {
		assert.FailNow(t, "Failed to convert account ID to UUID")
	}
	portfolio := createTestPortfolio(t, storageService, user.Id)
	portfolioUUID, err := stringToUUID(portfolio.Id)
	if !assert.NoError(t, err) {
		assert.FailNow(t, "Failed to convert portfolio ID to UUID")
	}
	asset := createTestAsset(t, storageService, "TestAsset")
	assetUUID, err := stringToUUID(asset.Id)
	if !assert.NoError(t, err) {
		assert.FailNow(t, "Failed to convert asset ID to UUID")
	}

	t.Run("Convert correct ent holding", func(t *testing.T) {
		entHolding := &ent.Holding{
			UUID:      uuid.New(),
			Amount:    100_000_000,
			Decimals:  8,
			CreatedAt: time.Unix(9, 0),
			UpdatedAt: time.Unix(10, 0),
			Edges: ent.HoldingEdges{
				Asset:     &ent.Asset{UUID: assetUUID},
				Account:   &ent.Account{UUID: accountUUID},
				Portfolio: &ent.Portfolio{UUID: portfolioUUID},
			},
		}

		protoHolding, err := entHoldingToProtoHolding(entHolding)
		if !assert.NoError(t, err) {
			assert.FailNow(t, "Failed to convert holding")
		}
		assert.Equal(t, entHolding.UUID.String(), protoHolding.Id)
		assert.Equal(t, entHolding.Amount, protoHolding.Amount)
		assert.Equal(t, entHolding.Decimals, protoHolding.Decimals)
		assert.Equal(t, asset.Id, protoHolding.AssetId)
		assert.Equal(t, account.Id, protoHolding.AccountId)
		if assert.NotNil(t, protoHolding.PortfolioId) {
			assert.Equal(t, portfolio.Id, *protoHolding.PortfolioId)
		}
	})
}

func TestEntPortfolioToProtoPortfolio(t *testing.T) {
	t.Run("Convert correct ent portfolio", func(t *testing.T) {
		userEnt := &ent.User{UUID: uuid.New()}
		entPortfolio := &ent.Portfolio{
			UUID:        uuid.New(),
			Name:        "Long Term",
			Description: "My portfolio",
			CreatedAt:   time.Unix(13, 0),
			UpdatedAt:   time.Unix(14, 0),
			Edges: ent.PortfolioEdges{
				User: userEnt,
			},
		}
		p, err := entPortfolioToProtoPortfolio(entPortfolio)
		if !assert.NoError(t, err) {
			assert.FailNow(t, "Failed to convert portfolio")
		}
		assert.Equal(t, entPortfolio.UUID.String(), p.Id)
		assert.Equal(t, "Long Term", p.Name)
		assert.Equal(t, "My portfolio", *p.Description)
		assert.Equal(t, userEnt.UUID.String(), p.UserId)
	})
}

func TestEntPriceToProtoPrice(t *testing.T) {
	storageService := getTransactionedService(t)
	asset := createTestAsset(t, storageService, "TestAsset")
	assetUUID, err := stringToUUID(asset.Id)
	if !assert.NoError(t, err) {
		assert.FailNow(t, "Failed to convert asset ID to UUID")
	}
	baseAsset := createTestAsset(t, storageService, "TestBaseAsset")
	baseAssetUUID, err := stringToUUID(baseAsset.Id)
	if !assert.NoError(t, err) {
		assert.FailNow(t, "Failed to convert base asset ID to UUID")
	}

	t.Run("Convert correct ent price", func(t *testing.T) {
		entPrice := &ent.Price{
			UUID:     uuid.New(),
			SourceID: "coingecko",
			Interval: "1h",
			Decimals: 2,
			Edges: ent.PriceEdges{
				Asset:     &ent.Asset{UUID: assetUUID},
				BaseAsset: &ent.Asset{UUID: baseAssetUUID},
			},
		}
		protoPrice, err := entPriceToProtoPrice(entPrice)
		if !assert.NoError(t, err) {
			assert.FailNow(t, "Failed to convert price")
		}
		assert.Equal(t, entPrice.UUID.String(), protoPrice.Id)
		assert.Equal(t, entPrice.SourceID, protoPrice.SourceId)
		assert.Equal(t, asset.Id, protoPrice.AssetId)
		assert.Equal(t, baseAsset.Id, protoPrice.BaseAssetId)
		assert.Equal(t, entPrice.Interval, protoPrice.Interval)
		assert.Equal(t, entPrice.Decimals, protoPrice.Decimals)
		assert.Equal(t, entPrice.Timestamp, protoPrice.Timestamp.AsTime())
	})
}

func TestEntTransactionToProtoTransaction(t *testing.T) {
	t.Run("Convert correct ent transaction", func(t *testing.T) {
		entTx := &ent.Transaction{
			UUID:      uuid.New(),
			Type:      transaction.TypeTrade,
			Status:    transaction.StatusCompleted,
			Data:      map[string]string{"key": "value"},
			CreatedAt: time.Now().UTC(),
			UpdatedAt: time.Now().UTC(),
			Edges: ent.TransactionEdges{
				Account: &ent.Account{
					UUID: uuid.New(),
					Name: "TestAccount",
					Edges: ent.AccountEdges{
						User: &ent.User{
							UUID: uuid.New(),
							Name: "TestUser",
						},
					},
				},
			},
		}
		protoTx, err := entTransactionToProtoTransaction(entTx)
		if !assert.NoError(t, err) || !assert.NotNil(t, protoTx) {
			assert.FailNow(t, "Failed to convert transaction to proto")
		}
		assert.Equal(t, entTx.UUID.String(), protoTx.Id)
		assert.Equal(t, models.TransactionType_TRANSACTION_TYPE_TRADE, protoTx.Type)
		assert.Equal(t, models.TransactionStatus_TRANSACTION_STATUS_COMPLETED, protoTx.Status)
		assert.Equal(t, entTx.Data, protoTx.Data)
		assert.Equal(t, entTx.CreatedAt, protoTx.CreatedAt.AsTime())
		assert.Equal(t, entTx.UpdatedAt, protoTx.UpdatedAt.AsTime())
	})
}
