package coingecko

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/foxcool/greedy-eye/internal/entity"
	"github.com/shopspring/decimal"
)

const (
	// ProviderName is the canonical source identifier for CoinGecko prices.
	ProviderName = "coingecko"

	sourceID      = ProviderName
	priceDecimals = uint32(8)
	interval      = "latest"

	// cgQuoteCurrency is the currency code used in CoinGecko API requests.
	// Prices are returned in USD; the internal base_asset_id UUID is stored separately.
	cgQuoteCurrency = "usd"
)

// nativeCoinID maps lowercase asset symbols → CoinGecko coin ID for native/major coins.
// Symbols are not unique on CoinGecko; this covers well-known cases.
var nativeCoinID = map[string]string{
	"eth":    "ethereum",
	"weth":   "weth",
	"steth":  "staked-ether",
	"wsteth": "wrapped-steth",
	"reth":   "rocket-pool-eth",
	"cbeth":  "coinbase-wrapped-staked-eth",
	"btc":    "bitcoin",
	"wbtc":   "wrapped-bitcoin",
	"bnb":    "binancecoin",
	"matic":  "matic-network",
	"pol":    "polygon-ecosystem-token",
	"avax":   "avalanche-2",
	"sol":    "solana",
	"ftm":    "fantom",
	"op":     "optimism",
	"arb":    "arbitrum",
	"usdt":   "tether",
	"usdc":   "usd-coin",
	"dai":    "dai",
	"busd":   "binance-usd",
	"frax":   "frax",
	"lusd":   "liquity-usd",
	"tusd":   "true-usd",
	"usdd":   "usdd",
	"link":   "chainlink",
	"uni":    "uniswap",
	"aave":   "aave",
	"mkr":    "maker",
	"comp":   "compound-governance-token",
	"crv":    "curve-dao-token",
	"cvx":    "convex-finance",
	"bal":    "balancer",
	"sushi":  "sushi",
	"xsushi": "xsushi",
	"1inch":  "1inch",
	"yfi":    "yearn-finance",
	"snx":    "havven",
	"ldo":    "lido-dao",
	"rpl":    "rocket-pool",
	"dydx":   "dydx",
	"imx":    "immutable-x",
	"grt":    "the-graph",
	"enj":    "enjincoin",
	"sand":   "the-sandbox",
	"mana":   "decentraland",
	"axs":    "axie-infinity",
	"chz":    "chiliz",
	"bat":    "basic-attention-token",
	"lrc":    "loopring",
	"zrx":    "0x",
	"shib":   "shiba-inu",
	"pepe":   "pepe",
	"doge":   "dogecoin",
	"atom":   "cosmos",
	"akt":    "akash-network",
	"osmo":   "osmosis",
	"dot":    "polkadot",
	"ada":    "cardano",
	"trx":    "tron",
	"xlm":    "stellar",
	"amb":    "amber",
	"ton":    "the-open-network",
	"tston":  "tonstakers",
	"xmr":    "monero",
	"xtz":    "tezos",
	"dash":   "dash",
	"glmr":   "moonbeam",
	"astr":   "astar",
	"ksm":    "kusama",
	"tao":    "bittensor",
	"hdx":    "hydradx",
	"velo":   "velo",
	"fil":    "filecoin",
	"ens":    "ethereum-name-service",
	"gtc":    "gitcoin",
	// Aave receipt tokens redeem 1:1 for the underlying; stkAAVE has no own
	// CoinGecko listing, so it is priced as AAVE.
	"stkaave":  "aave",
	"ausdc":    "aave-usdc",
	"adai":     "aave-dai",
	"alink":    "aave-link",
	"amkr":     "aave-mkr",
	"auni":     "aave-uni",
	"ayfi":     "aave-yfi",
	"aethusdc": "aave-v3-usdc",
	"aethdai":  "aave-v3-dai",
	"aethweth": "aave-v3-weth",
	"aoptdai":  "aave-v3-dai",
	"aweth":    "aave-v3-weth",
}

// Provider adapts *Client to marketdata.PriceProvider.
type Provider struct {
	client    *Client
	contracts *contractIndex
	log       *slog.Logger
}

// NewProvider wraps a *Client as a CoinGecko price provider.
func NewProvider(c *Client) *Provider {
	return &Provider{client: c, contracts: newContractIndex(c), log: slog.Default()}
}

// BudgetExemptSymbols reports the curated coins one /coins/markets request
// covers regardless of how many are asked for. Refreshing them costs a single
// request, so an unattended sweep does not budget them.
func (p *Provider) BudgetExemptSymbols() []string {
	out := make([]string, 0, len(nativeCoinID))
	for symbol := range nativeCoinID {
		out = append(out, strings.ToUpper(symbol))
	}
	return out
}

// AssetBudget converts what is left of the plan's period allowance into a
// number of assets this sweep may ask about.
//
// The share is proportional: spending the remainder evenly over the rest of the
// period is what keeps a monthly allowance from being gone in the first week.
// One request is reserved for the curated batch, and the rest buy contract
// lookups at this tier's batch size.
func (p *Provider) AssetBudget(now time.Time, window time.Duration) (int, bool) {
	remaining, periodEnd, ok := p.client.budget.Remaining()
	if !ok {
		return 0, false
	}

	left := periodEnd.Sub(now)
	if left <= 0 || window <= 0 {
		return 0, true
	}

	requests := int(float64(remaining) * (float64(window) / float64(left)))
	// Always leave room for the curated batch: it is one request and covers the
	// assets that matter most.
	if requests <= 1 {
		return 0, true
	}
	return (requests - 1) * p.client.contractBatchSize(), true
}

// BaseAssetSymbol returns the ticker of the quote currency used by CoinGecko ("USD").
func (p *Provider) BaseAssetSymbol() string { return "USD" }

// BaseAssetType reports that CoinGecko's quote currency (USD) is fiat (forex).
func (p *Provider) BaseAssetType() entity.AssetType { return entity.AssetTypeForex }

// FetchPrices fetches prices from CoinGecko for the given assets.
//
// Strategy:
//  1. Native/well-known coins → CoinGecko coin ID from the curated symbol map,
//     one /coins/markets request for all of them
//  2. Assets with a contract → token_price on the platform their chain maps to,
//     one request per platform; the chain comes from the asset's
//     "onchain:<chain>" external ref
//  3. Unknown symbols, contracts with no chain, and chains CoinGecko does not
//     list → skipped, because a guessed platform prices somebody else's token
func (p *Provider) FetchPrices(ctx context.Context, assets []*entity.Asset) ([]entity.StoredPrice, error) {
	if len(assets) == 0 {
		return nil, nil
	}

	// Split assets into two groups. Contract lookups are grouped by the
	// provider platform their chain maps to: one request per platform, and a
	// chain CoinGecko does not list is skipped rather than defaulted.
	type contractAsset struct {
		id      string
		address string
	}
	contractsByPlatform := map[string][]contractAsset{}
	var nativeAssets []*entity.Asset // native coins looked up by symbol
	var unmappedChains []string

	for _, a := range assets {
		// The curated symbol map wins over a contract tag: contract addresses
		// synced from L2 chains (e.g. OP on Optimism) are not listed under the
		// Ethereum platform, while the map targets the canonical coin ID.
		//
		// It applies to the global crypto market only. A per-contract row
		// (market "onchain:<chain>/<address>") is an unverified instrument that
		// merely carries a well-known ticker: pricing it from the symbol map is
		// exactly how a counterfeit USDT was valued as real USDT (personal-c3b).
		if _, ok := nativeCoinID[strings.ToLower(a.Symbol)]; ok && a.Market == entity.MarketCrypto {
			nativeAssets = append(nativeAssets, a)
			continue
		}

		addr := contractTag(a.Tags)
		if addr == "" {
			continue // unknown, skip — no reliable CoinGecko mapping
		}
		chain, ok := onchainChain(a)
		if !ok {
			// A contract with no chain cannot be routed. Guessing Ethereum is
			// how a Base address gets priced as an unrelated Ethereum token.
			continue
		}
		platform, ok := chainPlatform[chain]
		if !ok {
			unmappedChains = append(unmappedChains, chain)
			continue
		}
		contractsByPlatform[platform] = append(
			contractsByPlatform[platform], contractAsset{id: a.ID, address: addr})
	}

	now := time.Now()
	var result []entity.StoredPrice

	// --- Path 1: native / well-known coins by CoinGecko ID ---
	// Runs first: it is a single request covering the curated (most valuable)
	// assets, and must not be starved of rate budget by the per-contract
	// lookups below, which can burn many requests on the keyless tier.
	if len(nativeAssets) > 0 {
		// Several catalog assets may share one coin ID (e.g. duplicate USDT
		// rows) — fan the fetched price out to all of them.
		coinToAssets := make(map[string][]string, len(nativeAssets))
		coinIDs := make([]string, 0, len(nativeAssets))
		for _, a := range nativeAssets {
			cgID := nativeCoinID[strings.ToLower(a.Symbol)]
			if _, dup := coinToAssets[cgID]; !dup {
				coinIDs = append(coinIDs, cgID)
			}
			coinToAssets[cgID] = append(coinToAssets[cgID], a.ID)
		}

		raw, err := p.client.GetMultiplePrices(ctx, coinIDs, cgQuoteCurrency)
		if err != nil {
			return result, fmt.Errorf("coingecko native lookup: %w", err)
		}
		for cgID, pd := range raw {
			for _, assetID := range coinToAssets[cgID] {
				result = append(result, storedPrice(assetID, pd, now))
			}
		}
	}

	// --- Path 2: tokens by contract address, one request per platform ---
	for platform, cas := range contractsByPlatform {
		addrs := make([]string, len(cas))
		addrToID := make(map[string]string, len(cas))
		for i, ca := range cas {
			addrs[i] = ca.address
			addrToID[strings.ToLower(ca.address)] = ca.id
		}

		pricesByAddr, err := p.client.GetTokenPricesByContract(ctx, platform, addrs, cgQuoteCurrency)
		if err != nil {
			// Non-fatal: the map may still hold prices from succeeded batches,
			// and the native lookup above has already been collected.
			p.log.Warn("coingecko contract lookup partially failed",
				"platform", platform, "error", err)
		}
		for addr, pd := range pricesByAddr {
			assetID, ok := addrToID[strings.ToLower(addr)]
			if !ok {
				continue
			}
			result = append(result, storedPrice(assetID, pd, now))
		}
	}

	if len(unmappedChains) > 0 {
		// One line per sweep, not per asset: an unlisted chain is a routine
		// fact about the catalogue, not an incident.
		p.log.Debug("coingecko has no platform for these chains, assets skipped",
			"count", len(unmappedChains), "chains", slices.Compact(slices.Sorted(slices.Values(unmappedChains))))
	}

	return result, nil
}

// onchainChain reports the chain a contract-bearing asset lives on, read from
// its "onchain:<chain>" external ref. Refs are loaded only on the pricing path,
// so an asset without them yields false and is skipped rather than guessed at.
func onchainChain(a *entity.Asset) (string, bool) {
	for _, ref := range a.ExternalRefs {
		if chain, ok := entity.ChainFromOnchainSource(ref.Source); ok {
			return chain, true
		}
	}
	return "", false
}

// contractTag extracts the contract address from a "contract:0x..." tag, or returns "".
func contractTag(tags []string) string {
	for _, t := range tags {
		if after, ok := strings.CutPrefix(t, "contract:"); ok {
			return after
		}
	}
	return ""
}

// scaled converts a float price to a raw integer scaled by priceDecimals as a decimal.
func scaled(v float64) decimal.Decimal {
	return decimal.NewFromFloat(v).Shift(int32(priceDecimals)).Round(0)
}

func storedPrice(assetID string, pd *PriceData, now time.Time) entity.StoredPrice {
	ts := pd.Timestamp
	if ts.IsZero() {
		ts = now
	}
	return entity.StoredPrice{
		SourceID: sourceID,
		AssetID:  assetID,
		// BaseAssetID is intentionally empty — resolved by FetchExternalPrices handler.
		Interval:  interval,
		Decimals:  priceDecimals,
		Last:      scaled(pd.Price),
		High:      decimal.NullDecimal{Decimal: scaled(pd.High24h), Valid: true},
		Low:       decimal.NullDecimal{Decimal: scaled(pd.Low24h), Valid: true},
		Timestamp: ts,
	}
}
