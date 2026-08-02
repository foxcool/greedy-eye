package main

import (
	"context"
	"encoding/base64"
	"fmt"
	"log/slog"

	storecrypto "github.com/foxcool/greedy-eye/internal/store/crypto"
	"github.com/foxcool/greedy-eye/internal/store/postgres"
)

// encryptionOpts builds the store's encryption options from config (ADR-005).
//
// The previous key, when set, only widens what can be READ: writes always use
// the current key. That is what lets a rotation be two steps instead of one
// atomic act — see rewrapSecrets.
func encryptionOpts(config *Config, log *slog.Logger) ([]postgres.PortfolioStoreOption, error) {
	if config.Security.MasterKey == "" {
		if config.Security.PreviousMasterKey != "" {
			return nil, fmt.Errorf("security.previousMasterKey is set without a current master key")
		}
		log.Warn("SECURITY_MASTERKEY is not set: accounts.data is stored in plaintext")
		return nil, nil
	}

	masterKey, err := base64.StdEncoding.DecodeString(config.Security.MasterKey)
	if err != nil {
		return nil, fmt.Errorf("decode security master key: %w", err)
	}
	encryptor, err := storecrypto.NewEncryptor(masterKey)
	if err != nil {
		return nil, fmt.Errorf("init encryptor: %w", err)
	}

	if config.Security.PreviousMasterKey != "" {
		previous, err := base64.StdEncoding.DecodeString(config.Security.PreviousMasterKey)
		if err != nil {
			return nil, fmt.Errorf("decode previous security master key: %w", err)
		}
		encryptor, err = encryptor.WithPreviousKey(previous)
		if err != nil {
			return nil, fmt.Errorf("init encryptor with previous key: %w", err)
		}
		log.Warn("a previous master key is configured: rows sealed under it are still readable. " +
			"Run `eye rewrap-secrets` and then remove SECURITY_PREVIOUSMASTERKEY")
	}

	return []postgres.PortfolioStoreOption{postgres.WithEncryptor(encryptor)}, nil
}

// rewrapSecrets re-encrypts every accounts.data row under the current master key
// and exits. It is the second half of a key rotation, and it is a separate
// command on purpose: rewriting every credential in the database is an operator
// decision, not something a deploy should do on its way up.
//
// The full rotation, in order — the order matters, because the previous key is
// the only thing that can read the rows written before the change:
//
//  1. put the new key in SECURITY_MASTERKEY and the old one in
//     SECURITY_PREVIOUSMASTERKEY;
//  2. restart — the instance now writes new, reads either;
//  3. run `eye rewrap-secrets`;
//  4. drop SECURITY_PREVIOUSMASTERKEY and restart again.
//
// Skipping straight to step 4 is what makes the data unrecoverable, so the
// command refuses to report success on a row it could not open.
func rewrapSecrets(ctx context.Context, config *Config, log *slog.Logger) error {
	opts, err := encryptionOpts(config, log)
	if err != nil {
		return err
	}
	if len(opts) == 0 {
		return fmt.Errorf("rewrap-secrets needs SECURITY_MASTERKEY; nothing to re-encrypt in plaintext mode")
	}

	pool, err := postgres.NewPool(ctx, config.DB.URL)
	if err != nil {
		return fmt.Errorf("connect to database: %w", err)
	}
	defer pool.Close()

	res, err := postgres.NewPortfolioStore(pool, opts...).RewrapAccountData(ctx)
	// The counters are reported even on failure: knowing how far a partial pass
	// got is the difference between rerunning it and investigating one row.
	log.Info("rewrap-secrets finished",
		"scanned", res.Scanned, "rewritten", res.Rewritten, "skipped_empty", res.Skipped)
	if err != nil {
		return fmt.Errorf("rewrap accounts.data: %w", err)
	}
	if config.Security.PreviousMasterKey != "" {
		log.Info("every row is now sealed under the current key: remove SECURITY_PREVIOUSMASTERKEY and restart")
	}
	return nil
}
