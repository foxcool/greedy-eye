// Package solana adapts the Solana JSON-RPC and Helius DAS APIs to
// entity.WalletSyncer, covering native SOL and SPL token balances.
package solana

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// ProviderName is the canonical provider slug for these credentials. Solana's
// public RPC is rate-limited far below what periodic syncing needs, so the
// practical route is a Helius key — and the slug names whoever holds the
// credentials, as it does for the other adapters.
const ProviderName = "helius"

// Chain is the single chain identifier this adapter serves.
const Chain = "solana"

const (
	nativeSymbol   = "SOL"
	nativeName     = "Solana"
	nativeDecimals = 9
)

// Token program ids. Both must be queried: Token-2022 is a separate program
// with its own accounts, and a wallet holding a Token-2022 asset would
// otherwise report nothing for it.
const (
	// #nosec G101 -- public on-chain program addresses, the same for everyone
	tokenProgram = "TokenkegQfeZyiNwAJbNbGKPFXCWuBvf9Ss623VQ5DA"
	// #nosec G101 -- public on-chain program address, not a credential
	token2022Program = "TokenzQdBNbLqP5VEhdkAS6EPFLC1PHnBqCXEpPxuEb"
)

// Config holds Solana client configuration.
type Config struct {
	APIKey string

	// Transport, when set, replaces the client's HTTP transport. The shared
	// provider rate budget (internal/adapter/ratelimit) is injected here:
	// clients are built per account and must not each pace themselves.
	Transport http.RoundTripper
}

// Client talks to a Solana JSON-RPC endpoint (Helius, which also serves the
// DAS metadata methods on the same URL).
type Client struct {
	apiKey     string
	baseURL    string
	httpClient *http.Client
}

// NewClient creates a Solana client. Without a key it points at the public RPC,
// which answers but throttles hard; the DAS methods are Helius-only, so token
// symbols will be missing there.
func NewClient(cfg Config) *Client {
	baseURL := "https://api.mainnet-beta.solana.com"
	if cfg.APIKey != "" {
		baseURL = "https://mainnet.helius-rpc.com/?api-key=" + cfg.APIKey
	}
	return &Client{
		apiKey:     cfg.APIKey,
		baseURL:    baseURL,
		httpClient: &http.Client{Timeout: 30 * time.Second, Transport: cfg.Transport},
	}
}

// TokenAccount is one SPL token position held by a wallet.
type TokenAccount struct {
	Mint string
	// Amount is the raw integer balance scaled by Decimals.
	Amount   string
	Decimals int
}

// AssetMeta is the display metadata DAS holds for a mint. The RPC token
// accounts carry no symbol at all — only the mint — so without this a position
// would have nothing to price or label.
type AssetMeta struct {
	Symbol string
	Name   string
	// Burnt marks an asset the network considers destroyed.
	Burnt bool
}

// GetBalance returns the native SOL balance in lamports.
func (c *Client) GetBalance(ctx context.Context, address string) (string, error) {
	var result struct {
		// json.Number, not int64: lamports are a raw u64 and must not
		// round-trip through float64.
		Value json.Number `json:"value"`
	}
	if err := c.call(ctx, "getBalance", []any{address}, &result); err != nil {
		return "", err
	}
	if result.Value == "" {
		return "0", nil
	}
	return result.Value.String(), nil
}

// GetTokenAccounts returns the wallet's SPL positions across both token
// programs. A failure on one program is fatal for the call: a partial token
// list is indistinguishable from a wallet that sold the missing positions.
func (c *Client) GetTokenAccounts(ctx context.Context, owner string) ([]TokenAccount, error) {
	var accounts []TokenAccount
	for _, program := range []string{tokenProgram, token2022Program} {
		var result struct {
			Value []struct {
				Account struct {
					Data struct {
						Parsed struct {
							Info struct {
								Mint        string `json:"mint"`
								TokenAmount struct {
									Amount   string `json:"amount"`
									Decimals int    `json:"decimals"`
								} `json:"tokenAmount"`
							} `json:"info"`
						} `json:"parsed"`
					} `json:"data"`
				} `json:"account"`
			} `json:"value"`
		}

		params := []any{
			owner,
			map[string]string{"programId": program},
			map[string]string{"encoding": "jsonParsed"},
		}
		if err := c.call(ctx, "getTokenAccountsByOwner", params, &result); err != nil {
			return nil, fmt.Errorf("token program %s: %w", program, err)
		}

		for _, v := range result.Value {
			info := v.Account.Data.Parsed.Info
			accounts = append(accounts, TokenAccount{
				Mint:     info.Mint,
				Amount:   info.TokenAmount.Amount,
				Decimals: info.TokenAmount.Decimals,
			})
		}
	}
	return accounts, nil
}

// GetAssetMetadata resolves mints to their symbols in one DAS call. Mints the
// index does not know are simply absent from the result.
func (c *Client) GetAssetMetadata(ctx context.Context, mints []string) (map[string]AssetMeta, error) {
	if len(mints) == 0 {
		return nil, nil
	}

	var result []struct {
		ID      string `json:"id"`
		Burnt   bool   `json:"burnt"`
		Content struct {
			Metadata struct {
				Symbol string `json:"symbol"`
				Name   string `json:"name"`
			} `json:"metadata"`
		} `json:"content"`
		TokenInfo struct {
			Symbol string `json:"symbol"`
		} `json:"token_info"`
	}
	if err := c.call(ctx, "getAssetBatch", map[string]any{"ids": mints}, &result); err != nil {
		return nil, err
	}

	meta := make(map[string]AssetMeta, len(result))
	for _, a := range result {
		symbol := a.TokenInfo.Symbol
		if symbol == "" {
			symbol = a.Content.Metadata.Symbol
		}
		meta[a.ID] = AssetMeta{
			Symbol: symbol,
			Name:   a.Content.Metadata.Name,
			Burnt:  a.Burnt,
		}
	}
	return meta, nil
}

// call performs one JSON-RPC request and decodes result into out.
func (c *Client) call(ctx context.Context, method string, params any, out any) error {
	body, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      "greedy-eye",
		"method":  method,
		"params":  params,
	})
	if err != nil {
		return fmt.Errorf("encode request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("do request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("solana rpc status %d for %s", resp.StatusCode, method)
	}

	// JSON-RPC reports failures inside a 200 response, so the envelope must be
	// inspected rather than the status code.
	var envelope struct {
		Result json.RawMessage `json:"result"`
		Error  *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		return fmt.Errorf("decode %s: %w", method, err)
	}
	if envelope.Error != nil {
		return fmt.Errorf("solana rpc error for %s: code %d: %s",
			method, envelope.Error.Code, envelope.Error.Message)
	}
	if len(envelope.Result) == 0 {
		return nil
	}
	if err := json.Unmarshal(envelope.Result, out); err != nil {
		return fmt.Errorf("decode %s result: %w", method, err)
	}
	return nil
}
