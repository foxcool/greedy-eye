//go:build integration

package postgres

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/foxcool/greedy-eye/internal/entity"
	"github.com/foxcool/greedy-eye/internal/service/portfolio"
	"github.com/foxcool/greedy-eye/internal/store"
	storecrypto "github.com/foxcool/greedy-eye/internal/store/crypto"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
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

// TestConvertManualAccountToWallet pins the conversion path every non-EVM
// adapter needs (personal-feb): accounts entered by hand during the manual
// import become live-synced wallets once their chain has an adapter.
//
// Type and capabilities must move in one update — a wallet may not keep
// manual_positions, so splitting the change across two calls leaves the
// account in a state the merged validation rejects.
func TestConvertManualAccountToWallet(t *testing.T) {
	pool := getTestPool(t)
	users := NewUserStore(pool)
	s := NewPortfolioStore(pool)
	ctx := context.Background()

	user := createTestUser(t, users)

	manual, err := s.CreateAccount(ctx, &entity.Account{
		UserID:       user.ID,
		Name:         "dot-controller",
		Type:         entity.AccountTypeManual,
		Capabilities: []entity.AccountCapability{entity.CapabilityManualPositions},
	})
	require.NoError(t, err)

	// Flipping the type alone strands manual_positions on a wallet.
	_, err = s.UpdateAccount(ctx, &entity.Account{
		ID:   manual.ID,
		Type: entity.AccountTypeWallet,
	}, []string{"type"})
	require.ErrorIs(t, err, store.ErrInvalidArgument)

	converted, err := s.UpdateAccount(ctx, &entity.Account{
		ID:           manual.ID,
		Type:         entity.AccountTypeWallet,
		Capabilities: []entity.AccountCapability{entity.CapabilityPortfolioSync},
		Data:         map[string]string{"address": "15oF4u...", "chain": "polkadot"},
	}, []string{"type", "capabilities", "data"})
	require.NoError(t, err)

	assert.Equal(t, entity.AccountTypeWallet, converted.Type)
	assert.ElementsMatch(t, []entity.AccountCapability{entity.CapabilityPortfolioSync}, converted.Capabilities)
	assert.Equal(t, "polkadot", converted.Data["chain"], "chain drives syncer routing")
	assert.Equal(t, "15oF4u...", converted.Data["address"])
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

// TestRewrapAccountDataCompletesARotation walks the whole two-step rotation:
// rows sealed under the old key, an instance configured with new + previous,
// the rewrap pass, and finally an instance that knows only the new key.
//
// The last assertion is the one that matters. Until the pass runs, dropping the
// previous key makes the row unreadable — and because the store fails the whole
// account row on a decryption error, that takes the wallet address down with
// the credentials, not just the secret.
func TestRewrapAccountDataCompletesARotation(t *testing.T) {
	pool := getTestPool(t)
	users := NewUserStore(pool)
	ctx := context.Background()
	user := createTestUser(t, users)

	oldKey := bytes.Repeat([]byte{7}, 32)
	newKey := bytes.Repeat([]byte{9}, 32)
	encFor := func(t *testing.T, keys ...[]byte) *storecrypto.Encryptor {
		t.Helper()
		e, err := storecrypto.NewEncryptor(keys...)
		require.NoError(t, err)
		return e
	}

	data := map[string]string{"api_key": "top-secret", "address": "0xabc"}
	created, err := NewPortfolioStore(pool, WithEncryptor(encFor(t, oldKey))).
		CreateAccount(ctx, &entity.Account{
			UserID: user.ID,
			Name:   "sealed before rotation",
			Type:   entity.AccountTypeService,
			Data:   data,
		})
	require.NoError(t, err)

	// The new key alone cannot read it — this is the state a careless rotation
	// leaves the whole table in.
	_, err = NewPortfolioStore(pool, WithEncryptor(encFor(t, newKey))).GetAccount(ctx, created.ID)
	require.Error(t, err)

	// Configured with both, the instance keeps working while it is half rotated.
	rotating := NewPortfolioStore(pool, WithEncryptor(encFor(t, newKey, oldKey)))
	got, err := rotating.GetAccount(ctx, created.ID)
	require.NoError(t, err)
	assert.Equal(t, data, got.Data)

	res, err := rotating.RewrapAccountData(ctx)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, res.Scanned, 1)
	assert.GreaterOrEqual(t, res.Rewritten, 1)

	// After the pass the previous key is no longer load bearing.
	afterward, err := NewPortfolioStore(pool, WithEncryptor(encFor(t, newKey))).GetAccount(ctx, created.ID)
	require.NoError(t, err)
	assert.Equal(t, data, afterward.Data,
		"the row must open under the current key alone once it has been rewrapped")

	var rawData []byte
	require.NoError(t, pool.QueryRow(ctx, "SELECT data FROM accounts WHERE id = $1", created.ID).Scan(&rawData))
	assert.NotContains(t, string(rawData), "top-secret")
}

// TestRewrapAccountDataRewrapsEncryptedEmptyData is the regression for a pass
// that reported success and still left a row sealed under the retired key.
//
// An account with no data at all is still stored as a sealed blob — a manual
// account carries an encrypted `{}`. The first version of this pass treated an
// empty decrypted map as "nothing to seal" and skipped the row, so it was never
// re-sealed; dropping the previous key then made it unreadable, and with it the
// whole account. Emptiness of the plaintext says nothing about which key the
// ciphertext is under. Caught on the dev stand's gate.io account.
func TestRewrapAccountDataRewrapsEncryptedEmptyData(t *testing.T) {
	pool := getTestPool(t)
	users := NewUserStore(pool)
	ctx := context.Background()
	user := createTestUser(t, users)

	oldKey := bytes.Repeat([]byte{7}, 32)
	newKey := bytes.Repeat([]byte{9}, 32)
	oldEnc, err := storecrypto.NewEncryptor(oldKey)
	require.NoError(t, err)

	created, err := NewPortfolioStore(pool, WithEncryptor(oldEnc)).CreateAccount(ctx, &entity.Account{
		UserID: user.ID,
		Name:   "no data at all",
		Type:   entity.AccountTypeManual,
		Data:   map[string]string{},
	})
	require.NoError(t, err)

	rotatingEnc, err := storecrypto.NewEncryptor(newKey, oldKey)
	require.NoError(t, err)

	res, err := NewPortfolioStore(pool, WithEncryptor(rotatingEnc)).RewrapAccountData(ctx)
	require.NoError(t, err)
	assert.Equal(t, res.Scanned, res.Rewritten, "every scanned row must be re-sealed, empty data included")

	// The point: with the stale key gone, the row still opens. This is exactly
	// what VerifyAccountDataReadable checks after the rekey job's pass.
	checked, err := NewPortfolioStore(pool, WithEncryptor(rotatingEnc.Current())).
		VerifyAccountDataReadable(ctx)
	require.NoError(t, err, "an empty-data row left under the old key would surface here")
	assert.Equal(t, res.Scanned, checked)

	currentOnly, err := storecrypto.NewEncryptor(newKey)
	require.NoError(t, err)
	got, err := NewPortfolioStore(pool, WithEncryptor(currentOnly)).GetAccount(ctx, created.ID)
	require.NoError(t, err, "an empty-data row left under the old key takes the whole account down")
	assert.Empty(t, got.Data)
}

// TestRewrapAccountDataConvergesLegacyPlaintext: ADR-005 left pre-encryption
// rows readable and re-sealed them only if something happened to update them.
// The rewrap pass is what finally converges them.
func TestRewrapAccountDataConvergesLegacyPlaintext(t *testing.T) {
	pool := getTestPool(t)
	users := NewUserStore(pool)
	ctx := context.Background()
	user := createTestUser(t, users)

	// Write a plaintext row the way a pre-ADR-005 instance would have.
	plain, err := NewPortfolioStore(pool).CreateAccount(ctx, &entity.Account{
		UserID: user.ID,
		Name:   "legacy plaintext",
		Type:   entity.AccountTypeService,
		Data:   map[string]string{"api_key": "legacy-secret"},
	})
	require.NoError(t, err)

	var rawData []byte
	require.NoError(t, pool.QueryRow(ctx, "SELECT data FROM accounts WHERE id = $1", plain.ID).Scan(&rawData))
	require.Contains(t, string(rawData), "legacy-secret", "precondition: the row starts in plaintext")

	s := NewPortfolioStore(pool, WithEncryptor(newTestEncryptor(t)))
	_, err = s.RewrapAccountData(ctx)
	require.NoError(t, err)

	require.NoError(t, pool.QueryRow(ctx, "SELECT data FROM accounts WHERE id = $1", plain.ID).Scan(&rawData))
	assert.NotContains(t, string(rawData), "legacy-secret")
	assert.Contains(t, string(rawData), `"enc": "v1:`)

	got, err := s.GetAccount(ctx, plain.ID)
	require.NoError(t, err)
	assert.Equal(t, "legacy-secret", got.Data["api_key"])
}

// TestRewrapAccountDataRefusesPlaintextMode: a pass with no key to seal with
// would scan every row and change nothing, which reads as success.
func TestRewrapAccountDataRefusesPlaintextMode(t *testing.T) {
	_, err := NewPortfolioStore(getTestPool(t)).RewrapAccountData(context.Background())
	require.Error(t, err)
	assert.ErrorIs(t, err, store.ErrInvalidArgument)
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

	// Two rows for one (account, asset) pair that differ only by chain: the row
	// is the position, and the chain is part of what identifies it. A pre-chain
	// row reads back as empty — never as "eth".
	t.Run("chain round-trip and per-chain rows", func(t *testing.T) {
		var ids []string
		for _, chain := range []string{"eth", "base", ""} {
			created, err := s.CreateHolding(ctx, &entity.Holding{
				AssetID:   asset.ID,
				AccountID: account.ID,
				Amount:    decimal.RequireFromString("100"),
				Decimals:  8,
				Chain:     chain,
				Source:    entity.SourceSync,
			})
			require.NoError(t, err, "chain %q", chain)
			ids = append(ids, created.ID)

			got, err := s.GetHolding(ctx, created.ID)
			require.NoError(t, err)
			assert.Equal(t, chain, got.Chain)
		}

		listed, _, err := s.ListHoldings(ctx, portfolio.ListHoldingsOpts{AccountID: account.ID, PageSize: 100})
		require.NoError(t, err)
		byID := map[string]*entity.Holding{}
		for _, h := range listed {
			byID[h.ID] = h
		}
		require.Len(t, byID, len(listed))
		assert.Equal(t, "eth", byID[ids[0]].Chain)
		assert.Equal(t, "base", byID[ids[1]].Chain)
		assert.Empty(t, byID[ids[2]].Chain)

		// Adoption of a pre-chain row is an update of the chain field alone.
		adopting := byID[ids[2]]
		adopting.Chain = "arbitrum"
		updated, err := s.UpdateHolding(ctx, adopting, []string{"chain"})
		require.NoError(t, err)
		assert.Equal(t, "arbitrum", updated.Chain)
	})

	// Liquidity is a vocabulary column: unknown ("") is a legitimate state and
	// the default, a typo is not — it would read as a class nothing aggregates.
	t.Run("liquidity round-trip and vocabulary guard", func(t *testing.T) {
		for _, liquidity := range []entity.Liquidity{
			entity.LiquidityLiquid, entity.LiquidityStaked, entity.LiquidityUnbonding,
			entity.LiquidityLocked, entity.LiquidityVesting, entity.LiquidityUnknown,
		} {
			created, err := s.CreateHolding(ctx, &entity.Holding{
				AssetID:   asset.ID,
				AccountID: account.ID,
				Amount:    decimal.RequireFromString("100"),
				Decimals:  8,
				Chain:     "cosmos",
				Liquidity: liquidity,
				Source:    entity.SourceSync,
			})
			require.NoError(t, err, "liquidity %q", liquidity)

			got, err := s.GetHolding(ctx, created.ID)
			require.NoError(t, err)
			assert.Equal(t, liquidity, got.Liquidity)
		}

		_, err := s.CreateHolding(ctx, &entity.Holding{
			AssetID:   asset.ID,
			AccountID: account.ID,
			Decimals:  8,
			Liquidity: "mostly-liquid",
			Source:    entity.SourceSync,
		})
		require.ErrorIs(t, err, store.ErrInvalidArgument)
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

// TestInHoldingsTxRollsBackTheWholeSnapshot: a sync rewrites the picture of an
// account, so a run that dies halfway must leave the previous picture intact.
// Prod 2026-07-25 had the opposite: SyncAccount aborted at the caller's 10s
// deadline mid-write, and the account was left carrying rows from two different
// syncs, which reads as a complete portfolio and is not one.
func TestInHoldingsTxRollsBackTheWholeSnapshot(t *testing.T) {
	pool := getTestPool(t)
	users := NewUserStore(pool)
	s := NewPortfolioStore(pool)
	assets := NewMarketDataStore(pool)
	ctx := context.Background()

	user := createTestUser(t, users)
	account, err := s.CreateAccount(ctx, &entity.Account{
		UserID:       user.ID,
		Name:         "tx wallet",
		Type:         entity.AccountTypeWallet,
		Capabilities: []entity.AccountCapability{entity.CapabilityPortfolioSync},
	})
	require.NoError(t, err)

	asset, err := assets.CreateAsset(ctx, &entity.Asset{
		Symbol: "TXATOM",
		Name:   "Transaction ATOM",
		Type:   entity.AssetTypeCryptocurrency,
	})
	require.NoError(t, err)

	// The snapshot the account already has: one row the next sync will refresh.
	existing, err := s.CreateHolding(ctx, &entity.Holding{
		AssetID:   asset.ID,
		AccountID: account.ID,
		Amount:    decimal.RequireFromString("100"),
		Decimals:  6,
		Liquidity: entity.LiquidityLiquid,
		Source:    entity.SourceSync,
	})
	require.NoError(t, err)

	t.Run("a failed write undoes the ones before it", func(t *testing.T) {
		wantErr := errors.New("provider row rejected")
		err := s.InHoldingsTx(ctx, func(w portfolio.HoldingWriter) error {
			existing.Amount = decimal.RequireFromString("999")
			if _, err := w.UpdateHolding(ctx, existing, []string{"amount"}); err != nil {
				return err
			}
			if _, err := w.CreateHolding(ctx, &entity.Holding{
				AssetID:   asset.ID,
				AccountID: account.ID,
				Amount:    decimal.RequireFromString("42"),
				Decimals:  6,
				Liquidity: entity.LiquidityStaked,
				Source:    entity.SourceSync,
			}); err != nil {
				return err
			}
			return wantErr // the sync gave up here — nothing above may survive
		})
		require.ErrorIs(t, err, wantErr)

		got, err := s.GetHolding(ctx, existing.ID)
		require.NoError(t, err)
		assert.Equal(t, "100", got.Amount.String(), "the update rolled back with the set")

		rows, _, err := s.ListHoldings(ctx, portfolio.ListHoldingsOpts{AccountID: account.ID, PageSize: 100})
		require.NoError(t, err)
		assert.Len(t, rows, 1, "the staked row created before the failure rolled back too")
	})

	t.Run("a clean run commits every row", func(t *testing.T) {
		err := s.InHoldingsTx(ctx, func(w portfolio.HoldingWriter) error {
			existing.Amount = decimal.RequireFromString("250")
			if _, err := w.UpdateHolding(ctx, existing, []string{"amount"}); err != nil {
				return err
			}
			_, err := w.CreateHolding(ctx, &entity.Holding{
				AssetID:   asset.ID,
				AccountID: account.ID,
				Amount:    decimal.RequireFromString("42"),
				Decimals:  6,
				Liquidity: entity.LiquidityStaked,
				Source:    entity.SourceSync,
			})
			return err
		})
		require.NoError(t, err)

		got, err := s.GetHolding(ctx, existing.ID)
		require.NoError(t, err)
		assert.Equal(t, "250", got.Amount.String())

		rows, _, err := s.ListHoldings(ctx, portfolio.ListHoldingsOpts{AccountID: account.ID, PageSize: 100})
		require.NoError(t, err)
		assert.Len(t, rows, 2)
	})
}

// TestListStaleSyncTargets covers the selection the balance sweep runs on: which
// accounts are due, in what order, and which are never swept at all.
func TestListStaleSyncTargets(t *testing.T) {
	pool := getTestPool(t)
	users := NewUserStore(pool)
	s := NewPortfolioStore(pool)
	assets := NewMarketDataStore(pool)
	ctx := context.Background()

	user := createTestUser(t, users)
	asset, err := assets.CreateAsset(ctx, &entity.Asset{
		Symbol: "STALEBTC", Name: "Stale BTC", Type: entity.AssetTypeCryptocurrency,
	})
	require.NoError(t, err)

	account := func(name string, typ entity.AccountType) *entity.Account {
		a, err := s.CreateAccount(ctx, &entity.Account{UserID: user.ID, Name: name, Type: typ})
		require.NoError(t, err)
		return a
	}
	// holdingAt writes a holding and backdates it: updated_at is set by the
	// store, and the whole question here is how old that timestamp is.
	holdingAt := func(accountID string, updatedAt time.Time) {
		h, err := s.CreateHolding(ctx, &entity.Holding{
			AssetID: asset.ID, AccountID: accountID, Decimals: 8, Source: entity.SourceSync,
		})
		require.NoError(t, err)
		_, err = pool.Exec(ctx, "UPDATE holdings SET updated_at = $1 WHERE id = $2", updatedAt, h.ID)
		require.NoError(t, err)
	}

	now := time.Now()
	fresh := account("fresh wallet", entity.AccountTypeWallet)
	holdingAt(fresh.ID, now.Add(-1*time.Hour))

	stale := account("stale wallet", entity.AccountTypeWallet)
	holdingAt(stale.ID, now.Add(-30*time.Hour))

	stalest := account("stalest exchange", entity.AccountTypeExchange)
	holdingAt(stalest.ID, now.Add(-200*time.Hour))

	neverSynced := account("never synced wallet", entity.AccountTypeWallet)

	manual := account("manual stash", entity.AccountTypeManual)
	holdingAt(manual.ID, now.Add(-500*time.Hour))

	got, err := s.ListStaleSyncTargets(ctx, now.Add(-12*time.Hour), 10)
	require.NoError(t, err)

	ids := make([]string, 0, len(got))
	for _, a := range got {
		ids = append(ids, a.ID)
	}
	assert.NotContains(t, ids, fresh.ID, "an account synced within the window is not due")
	assert.NotContains(t, ids, manual.ID, "a manual account has no provider to refresh it")
	require.Contains(t, ids, neverSynced.ID)
	require.Contains(t, ids, stalest.ID)
	require.Contains(t, ids, stale.ID)

	// Never synced sorts first — it is the stalest state there is, not a fresh
	// one — then oldest confirmation first.
	assert.Equal(t, neverSynced.ID, ids[0])
	assert.Less(t, indexOf(ids, stalest.ID), indexOf(ids, stale.ID),
		"the account confirmed longest ago comes first")

	t.Run("limit is the provider budget", func(t *testing.T) {
		limited, err := s.ListStaleSyncTargets(ctx, now.Add(-12*time.Hour), 2)
		require.NoError(t, err)
		require.Len(t, limited, 2, "a sweep takes what it can afford, the rest stays due")
		assert.Equal(t, neverSynced.ID, limited[0].ID)
	})

	t.Run("rejects a non-positive limit", func(t *testing.T) {
		_, err := s.ListStaleSyncTargets(ctx, now, 0)
		assert.ErrorIs(t, err, store.ErrInvalidArgument)
	})
}

func indexOf(ids []string, id string) int {
	for i, v := range ids {
		if v == id {
			return i
		}
	}
	return -1
}
