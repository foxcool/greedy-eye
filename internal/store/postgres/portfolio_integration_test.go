//go:build integration

package postgres

import (
	"bytes"
	"context"
	"testing"

	"github.com/foxcool/greedy-eye/internal/entity"
	"github.com/foxcool/greedy-eye/internal/service/portfolio"
	"github.com/foxcool/greedy-eye/internal/store"
	storecrypto "github.com/foxcool/greedy-eye/internal/store/crypto"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func createTestUser(t *testing.T, s *UserStore) *entity.User {
	t.Helper()
	id := uuid.Must(uuid.NewV7()).String()
	user, err := s.GetOrCreate(context.Background(), id, id+"@test.local")
	require.NoError(t, err, "user creation failed")
	return user
}

func TestAccountCapabilitiesRoundtrip(t *testing.T) {
	pool := getTestPool(t)
	users := NewUserStore(pool)
	s := NewPortfolioStore(pool)
	ctx := context.Background()

	user := createTestUser(t, users)

	created, err := s.CreateAccount(ctx, &entity.Account{
		UserID:       user.ID,
		Name:         "shared moralis key",
		Type:         entity.AccountTypeService,
		Data:         map[string]string{"api_key": "secret"},
		Capabilities: []entity.AccountCapability{entity.CapabilityOnchainLookup, entity.CapabilityMarketData},
		SystemScopes: []entity.AccountCapability{entity.CapabilityOnchainLookup},
	})
	require.NoError(t, err)

	got, err := s.GetAccount(ctx, created.ID)
	require.NoError(t, err)
	assert.Equal(t, entity.AccountTypeService, got.Type)
	assert.ElementsMatch(t, []entity.AccountCapability{entity.CapabilityOnchainLookup, entity.CapabilityMarketData}, got.Capabilities)
	assert.ElementsMatch(t, []entity.AccountCapability{entity.CapabilityOnchainLookup}, got.SystemScopes)

	// Accounts created without capabilities load as empty, not nil-breaking.
	plain, err := s.CreateAccount(ctx, &entity.Account{
		UserID: user.ID,
		Name:   "plain wallet",
		Type:   entity.AccountTypeWallet,
		Data:   map[string]string{"address": "0xabc"},
	})
	require.NoError(t, err)
	gotPlain, err := s.GetAccount(ctx, plain.ID)
	require.NoError(t, err)
	assert.Empty(t, gotPlain.Capabilities)
	assert.Empty(t, gotPlain.SystemScopes)
}

func TestCreateAccountRejectsInvalidCapabilities(t *testing.T) {
	pool := getTestPool(t)
	users := NewUserStore(pool)
	s := NewPortfolioStore(pool)
	ctx := context.Background()

	user := createTestUser(t, users)

	_, err := s.CreateAccount(ctx, &entity.Account{
		UserID:       user.ID,
		Name:         "wallet cannot trade",
		Type:         entity.AccountTypeWallet,
		Capabilities: []entity.AccountCapability{entity.CapabilityTrading},
	})
	require.ErrorIs(t, err, store.ErrInvalidArgument)
}

func TestUpdateAccountValidatesMergedCapabilities(t *testing.T) {
	pool := getTestPool(t)
	users := NewUserStore(pool)
	s := NewPortfolioStore(pool)
	ctx := context.Background()

	user := createTestUser(t, users)

	created, err := s.CreateAccount(ctx, &entity.Account{
		UserID:       user.ID,
		Name:         "exchange",
		Type:         entity.AccountTypeExchange,
		Capabilities: []entity.AccountCapability{entity.CapabilityTrading, entity.CapabilityMarketData},
		SystemScopes: []entity.AccountCapability{entity.CapabilityMarketData},
	})
	require.NoError(t, err)

	// Dropping market_data from capabilities would orphan the existing system scope.
	_, err = s.UpdateAccount(ctx, &entity.Account{
		ID:           created.ID,
		Capabilities: []entity.AccountCapability{entity.CapabilityTrading},
	}, []string{"capabilities"})
	require.ErrorIs(t, err, store.ErrInvalidArgument)

	// Dropping both together is consistent and allowed.
	updated, err := s.UpdateAccount(ctx, &entity.Account{
		ID:           created.ID,
		Capabilities: []entity.AccountCapability{entity.CapabilityTrading},
		SystemScopes: []entity.AccountCapability{},
	}, []string{"capabilities", "system_scopes"})
	require.NoError(t, err)
	assert.ElementsMatch(t, []entity.AccountCapability{entity.CapabilityTrading}, updated.Capabilities)
	assert.Empty(t, updated.SystemScopes)
}

func TestListSystemAccountsByCapability(t *testing.T) {
	pool := getTestPool(t)
	users := NewUserStore(pool)
	s := NewPortfolioStore(pool)
	ctx := context.Background()

	admin := createTestUser(t, users)
	other := createTestUser(t, users)

	shared, err := s.CreateAccount(ctx, &entity.Account{
		UserID:       admin.ID,
		Name:         "system moralis",
		Type:         entity.AccountTypeService,
		Capabilities: []entity.AccountCapability{entity.CapabilityOnchainLookup},
		SystemScopes: []entity.AccountCapability{entity.CapabilityOnchainLookup},
	})
	require.NoError(t, err)

	// Personal capability without system scope must not be returned.
	_, err = s.CreateAccount(ctx, &entity.Account{
		UserID:       other.ID,
		Name:         "personal moralis",
		Type:         entity.AccountTypeService,
		Capabilities: []entity.AccountCapability{entity.CapabilityOnchainLookup},
	})
	require.NoError(t, err)

	got, err := s.ListSystemAccountsByCapability(ctx, entity.CapabilityOnchainLookup)
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, shared.ID, got[0].ID)

	empty, err := s.ListSystemAccountsByCapability(ctx, entity.CapabilityMarketData)
	require.NoError(t, err)
	assert.Empty(t, empty)

	_, err = s.ListSystemAccountsByCapability(ctx, "")
	require.ErrorIs(t, err, store.ErrInvalidArgument)
}

func TestListUserAccountsByCapability(t *testing.T) {
	pool := getTestPool(t)
	users := NewUserStore(pool)
	s := NewPortfolioStore(pool)
	ctx := context.Background()

	owner := createTestUser(t, users)
	other := createTestUser(t, users)

	mine, err := s.CreateAccount(ctx, &entity.Account{
		UserID:       owner.ID,
		Name:         "my moralis",
		Type:         entity.AccountTypeService,
		Capabilities: []entity.AccountCapability{entity.CapabilityOnchainLookup},
	})
	require.NoError(t, err)

	// Someone else's account with the same capability must not leak in.
	_, err = s.CreateAccount(ctx, &entity.Account{
		UserID:       other.ID,
		Name:         "foreign moralis",
		Type:         entity.AccountTypeService,
		Capabilities: []entity.AccountCapability{entity.CapabilityOnchainLookup},
	})
	require.NoError(t, err)

	got, err := s.ListUserAccountsByCapability(ctx, owner.ID, entity.CapabilityOnchainLookup)
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, mine.ID, got[0].ID)

	empty, err := s.ListUserAccountsByCapability(ctx, owner.ID, entity.CapabilityMarketData)
	require.NoError(t, err)
	assert.Empty(t, empty)

	_, err = s.ListUserAccountsByCapability(ctx, "", entity.CapabilityOnchainLookup)
	require.ErrorIs(t, err, store.ErrInvalidArgument)
}

func newTestEncryptor(t *testing.T) *storecrypto.Encryptor {
	t.Helper()
	e, err := storecrypto.NewEncryptor(bytes.Repeat([]byte{7}, 32))
	require.NoError(t, err)
	return e
}

func TestAccountDataEncryptionRoundtrip(t *testing.T) {
	pool := getTestPool(t)
	users := NewUserStore(pool)
	s := NewPortfolioStore(pool, WithEncryptor(newTestEncryptor(t)))
	ctx := context.Background()

	user := createTestUser(t, users)
	data := map[string]string{"api_key": "top-secret", "address": "0xabc"}

	created, err := s.CreateAccount(ctx, &entity.Account{
		UserID: user.ID,
		Name:   "encrypted account",
		Type:   entity.AccountTypeService,
		Data:   data,
	})
	require.NoError(t, err)

	// On disk: {"enc": "v1:..."} wrapper, no plaintext secrets.
	var rawData []byte
	require.NoError(t, pool.QueryRow(ctx, "SELECT data FROM accounts WHERE id = $1", created.ID).Scan(&rawData))
	assert.NotContains(t, string(rawData), "top-secret")
	assert.Contains(t, string(rawData), `"enc": "v1:`)

	// Through the store: transparent decryption.
	got, err := s.GetAccount(ctx, created.ID)
	require.NoError(t, err)
	assert.Equal(t, data, got.Data)

	// Update re-encrypts.
	updatedData := map[string]string{"api_key": "rotated-secret"}
	updated, err := s.UpdateAccount(ctx, &entity.Account{ID: created.ID, Data: updatedData}, []string{"data"})
	require.NoError(t, err)
	assert.Equal(t, updatedData, updated.Data)
	require.NoError(t, pool.QueryRow(ctx, "SELECT data FROM accounts WHERE id = $1", created.ID).Scan(&rawData))
	assert.NotContains(t, string(rawData), "rotated-secret")

	// List paths decrypt too.
	listed, _, err := s.ListAccounts(ctx, portfolio.ListAccountsOpts{UserID: user.ID})
	require.NoError(t, err)
	require.Len(t, listed, 1)
	assert.Equal(t, updatedData, listed[0].Data)
}

func TestAccountDataLegacyPlaintextReadable(t *testing.T) {
	pool := getTestPool(t)
	users := NewUserStore(pool)
	plain := NewPortfolioStore(pool)
	encrypted := NewPortfolioStore(pool, WithEncryptor(newTestEncryptor(t)))
	ctx := context.Background()

	user := createTestUser(t, users)

	// Row written before encryption was enabled.
	legacy, err := plain.CreateAccount(ctx, &entity.Account{
		UserID: user.ID,
		Name:   "legacy plaintext",
		Type:   entity.AccountTypeWallet,
		Data:   map[string]string{"address": "0xlegacy"},
	})
	require.NoError(t, err)

	got, err := encrypted.GetAccount(ctx, legacy.ID)
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"address": "0xlegacy"}, got.Data)
}

func TestAccountDataEncryptedUnreadableWithoutKey(t *testing.T) {
	pool := getTestPool(t)
	users := NewUserStore(pool)
	encrypted := NewPortfolioStore(pool, WithEncryptor(newTestEncryptor(t)))
	plain := NewPortfolioStore(pool)
	ctx := context.Background()

	user := createTestUser(t, users)

	created, err := encrypted.CreateAccount(ctx, &entity.Account{
		UserID: user.ID,
		Name:   "sealed",
		Type:   entity.AccountTypeService,
		Data:   map[string]string{"api_key": "sealed-secret"},
	})
	require.NoError(t, err)

	_, err = plain.GetAccount(ctx, created.ID)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no master key")
}

func TestHoldingProvenanceRoundtrip(t *testing.T) {
	pool := getTestPool(t)
	users := NewUserStore(pool)
	s := NewPortfolioStore(pool)
	assets := NewMarketDataStore(pool)
	ctx := context.Background()

	user := createTestUser(t, users)

	account, err := s.CreateAccount(ctx, &entity.Account{
		UserID:       user.ID,
		Name:         "manual stash",
		Type:         entity.AccountTypeManual,
		Capabilities: []entity.AccountCapability{entity.CapabilityManualPositions},
	})
	require.NoError(t, err)

	asset, err := assets.CreateAsset(ctx, &entity.Asset{
		Symbol: "PROVBTC",
		Name:   "Provenance BTC",
		Type:   entity.AssetTypeCryptocurrency,
	})
	require.NoError(t, err)

	t.Run("each source persists and reads back", func(t *testing.T) {
		for _, source := range []entity.ProvenanceSource{entity.SourceSync, entity.SourceManual, entity.SourceLLMImport} {
			created, err := s.CreateHolding(ctx, &entity.Holding{
				AssetID:   asset.ID,
				AccountID: account.ID,
				Decimals:  8,
				Source:    source,
			})
			require.NoError(t, err, "source %s", source)

			got, err := s.GetHolding(ctx, created.ID)
			require.NoError(t, err)
			assert.Equal(t, source, got.Source)
			assert.Empty(t, got.ImportID)
		}
	})

	t.Run("empty source rejected", func(t *testing.T) {
		_, err := s.CreateHolding(ctx, &entity.Holding{
			AssetID:   asset.ID,
			AccountID: account.ID,
			Decimals:  8,
		})
		require.ErrorIs(t, err, store.ErrInvalidArgument)
	})

	t.Run("unknown source rejected", func(t *testing.T) {
		_, err := s.CreateHolding(ctx, &entity.Holding{
			AssetID:   asset.ID,
			AccountID: account.ID,
			Decimals:  8,
			Source:    "spoofed",
		})
		require.ErrorIs(t, err, store.ErrInvalidArgument)
	})

	t.Run("import_id round-trip and list scan", func(t *testing.T) {
		importID := uuid.Must(uuid.NewV7()).String()
		created, err := s.CreateHolding(ctx, &entity.Holding{
			AssetID:   asset.ID,
			AccountID: account.ID,
			Decimals:  8,
			Source:    entity.SourceLLMImport,
			ImportID:  importID,
		})
		require.NoError(t, err)

		got, err := s.GetHolding(ctx, created.ID)
		require.NoError(t, err)
		assert.Equal(t, importID, got.ImportID)

		listed, _, err := s.ListHoldings(ctx, portfolio.ListHoldingsOpts{AccountID: account.ID, PageSize: 100})
		require.NoError(t, err)
		var found *entity.Holding
		for _, h := range listed {
			if h.ID == created.ID {
				found = h
			}
		}
		require.NotNil(t, found, "imported holding missing from list")
		assert.Equal(t, entity.SourceLLMImport, found.Source)
		assert.Equal(t, importID, found.ImportID)
	})

	t.Run("delete holding", func(t *testing.T) {
		created, err := s.CreateHolding(ctx, &entity.Holding{
			AssetID:   asset.ID,
			AccountID: account.ID,
			Decimals:  8,
			Source:    entity.SourceManual,
		})
		require.NoError(t, err)

		require.NoError(t, s.DeleteHolding(ctx, created.ID))
		_, err = s.GetHolding(ctx, created.ID)
		require.ErrorIs(t, err, store.ErrNotFound)
	})
}

func TestTransactionProvenanceRoundtrip(t *testing.T) {
	pool := getTestPool(t)
	users := NewUserStore(pool)
	s := NewPortfolioStore(pool)
	ctx := context.Background()

	user := createTestUser(t, users)

	account, err := s.CreateAccount(ctx, &entity.Account{
		UserID: user.ID,
		Name:   "manual tx stash",
		Type:   entity.AccountTypeManual,
	})
	require.NoError(t, err)

	created, err := s.CreateTransaction(ctx, &entity.Transaction{
		AccountID: account.ID,
		Type:      entity.TransactionTypeDeposit,
		Source:    entity.SourceManual,
	})
	require.NoError(t, err)

	got, err := s.GetTransaction(ctx, created.ID)
	require.NoError(t, err)
	assert.Equal(t, entity.SourceManual, got.Source)
	assert.Empty(t, got.ImportID)

	_, err = s.CreateTransaction(ctx, &entity.Transaction{
		AccountID: account.ID,
		Type:      entity.TransactionTypeDeposit,
	})
	require.ErrorIs(t, err, store.ErrInvalidArgument)
}
