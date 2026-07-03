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
