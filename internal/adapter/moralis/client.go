package moralis

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// Client implements BlockchainClient interface for Moralis
type Client struct {
	apiKey     string
	baseURL    string
	httpClient *http.Client
}

// ProviderName is the canonical provider slug for Moralis credentials.
const ProviderName = "moralis"

// Config holds Moralis client configuration
type Config struct {
	APIKey string
}

// Balance represents wallet balance for a token
type Balance struct {
	TokenAddress     string
	Symbol           string
	Name             string
	Decimals         int
	Balance          string // Raw balance as string to avoid precision loss
	Thumbnail        string
	PossibleSpam     bool // Moralis spam classification; scam tokens often clone real symbols
	VerifiedContract bool // Moralis contract verification; fake clones are unverified
}

// Transaction represents a blockchain transaction
type Transaction struct {
	Hash             string
	From             string
	To               string
	Value            string
	Gas              string
	GasPrice         string
	BlockNumber      int64
	BlockTimestamp   time.Time
	TransactionIndex int
	Status           string
}

// NFT represents an NFT token
type NFT struct {
	TokenAddress string
	TokenID      string
	Name         string
	Symbol       string
	TokenURI     string
	Metadata     map[string]interface{}
	Amount       string
}

// NewClient creates a new Moralis blockchain client
func NewClient(cfg Config) *Client {
	return &Client{
		apiKey:     cfg.APIKey,
		baseURL:    "https://deep-index.moralis.io/api/v2",
		httpClient: &http.Client{Timeout: 30 * time.Second},
	}
}

// doGetURL is like doGet but takes a full URL instead of a path suffix.
func (c *Client) doGetURL(ctx context.Context, url string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("X-API-Key", c.apiKey)
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("do request: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		_ = resp.Body.Close()
		return nil, fmt.Errorf("moralis API status %d for %s", resp.StatusCode, url)
	}
	return resp, nil
}

// moralisActiveChain is one entry from the /api/v2.2/wallets/{address}/chains response.
type moralisActiveChain struct {
	Chain   string `json:"chain"`
	ChainID string `json:"chain_id"`
	// Non-null only for chains where the wallet actually transacted.
	FirstTransaction json.RawMessage `json:"first_transaction"`
}

// candidateChains is the set Moralis is asked to probe for activity. Without an
// explicit chains parameter the endpoint only checks eth, hiding L2/sidechain
// balances. The chains endpoint rejects scroll/zksync/fantom even though other
// Moralis endpoints support them, so those stay out of the list.
var candidateChains = []string{
	"eth", "base", "arbitrum", "optimism", "linea",
	"polygon", "bsc", "avalanche",
}

// GetActiveChains returns the list of EVM chains where the address has had activity.
// Uses the Moralis v2.2 wallet chains endpoint.
func (c *Client) GetActiveChains(ctx context.Context, address string) ([]string, error) {
	params := make([]string, 0, len(candidateChains))
	for _, ch := range candidateChains {
		params = append(params, "chains%5B%5D="+ch) // chains[]=<chain>
	}
	url := fmt.Sprintf("https://deep-index.moralis.io/api/v2.2/wallets/%s/chains?%s",
		address, strings.Join(params, "&"))
	resp, err := c.doGetURL(ctx, url)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	var result struct {
		ActiveChains []moralisActiveChain `json:"active_chains"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode active chains: %w", err)
	}

	chains := make([]string, 0, len(result.ActiveChains))
	for _, ac := range result.ActiveChains {
		active := len(ac.FirstTransaction) > 0 && string(ac.FirstTransaction) != "null"
		if ac.Chain != "" && active {
			chains = append(chains, ac.Chain)
		}
	}
	return chains, nil
}

// moralisERC20Token is the JSON shape from /v2/{address}/erc20 endpoint.
type moralisERC20Token struct {
	TokenAddress     string `json:"token_address"`
	Symbol           string `json:"symbol"`
	Name             string `json:"name"`
	Decimals         int    `json:"decimals"`
	Balance          string `json:"balance"`
	Thumbnail        string `json:"thumbnail"`
	PossibleSpam     bool   `json:"possible_spam"`
	VerifiedContract bool   `json:"verified_contract"`
}

func (c *Client) doGet(ctx context.Context, path string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("X-API-Key", c.apiKey)
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("do request: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		_ = resp.Body.Close()
		return nil, fmt.Errorf("moralis API status %d for %s", resp.StatusCode, path)
	}
	return resp, nil
}

// GetWalletTokenBalances retrieves all ERC-20 token balances for a wallet.
func (c *Client) GetWalletTokenBalances(ctx context.Context, chain string, address string) ([]Balance, error) {
	resp, err := c.doGet(ctx, fmt.Sprintf("/%s/erc20?chain=%s", address, chain))
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	var tokens []moralisERC20Token
	if err := json.NewDecoder(resp.Body).Decode(&tokens); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	result := make([]Balance, 0, len(tokens))
	for _, t := range tokens {
		result = append(result, Balance(t))
	}
	return result, nil
}

// GetWalletBalance retrieves native token (ETH) balance for a wallet.
func (c *Client) GetWalletBalance(ctx context.Context, chain string, address string) (string, error) {
	resp, err := c.doGet(ctx, fmt.Sprintf("/%s/balance?chain=%s", address, chain))
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()

	var result struct {
		Balance string `json:"balance"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decode response: %w", err)
	}
	return result.Balance, nil
}

// GetWalletNFTs retrieves all NFTs owned by a wallet (stub).
func (c *Client) GetWalletNFTs(ctx context.Context, chain string, address string) ([]NFT, error) {
	return nil, fmt.Errorf("GetWalletNFTs not implemented")
}

// GetTransactionHistory retrieves transaction history for a wallet (stub).
func (c *Client) GetTransactionHistory(ctx context.Context, chain string, address string, limit int) ([]Transaction, error) {
	return nil, fmt.Errorf("GetTransactionHistory not implemented")
}

// GetTransaction retrieves details for a specific transaction (stub).
func (c *Client) GetTransaction(ctx context.Context, chain string, txHash string) (*Transaction, error) {
	return nil, fmt.Errorf("GetTransaction not implemented")
}

// GetTokenPrice retrieves current price for a token (stub).
func (c *Client) GetTokenPrice(ctx context.Context, chain string, tokenAddress string) (float64, error) {
	return 0, fmt.Errorf("GetTokenPrice not implemented")
}

// ValidateAddress verifies if an address is valid for the given chain (stub).
func (c *Client) ValidateAddress(ctx context.Context, chain string, address string) (bool, error) {
	return false, fmt.Errorf("ValidateAddress not implemented")
}

// GetBlockByNumber retrieves block information by block number (stub).
func (c *Client) GetBlockByNumber(ctx context.Context, chain string, blockNumber int64) (interface{}, error) {
	return nil, fmt.Errorf("GetBlockByNumber not implemented")
}
