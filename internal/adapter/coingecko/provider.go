package coingecko

import (
	"context"
	"fmt"
	"log/slog"
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

	// cgPlatformEVM is the CoinGecko platform ID used for EVM token contract lookups.
	// Most ERC-20 tokens on Ethereum, Arbitrum, Base share the same contract addresses
	// and are listed under the Ethereum platform on CoinGecko.
	cgPlatformEVM = "ethereum"
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

// BaseAssetSymbol returns the ticker of the quote currency used by CoinGecko ("USD").
func (p *Provider) BaseAssetSymbol() string { return "USD" }

// BaseAssetType reports that CoinGecko's quote currency (USD) is fiat (forex).
func (p *Provider) BaseAssetType() entity.AssetType { return entity.AssetTypeForex }

// FetchPrices fetches prices from CoinGecko for the given assets.
//
// Strategy:
//  1. Assets with a "contract:0x..." tag → CoinGecko token_price by contract address
//     (accurate for ERC-20 tokens whose contract address is on the Ethereum platform)
//  2. Native/well-known coins → CoinGecko coin ID from the hardcoded symbol map
//  3. Unknown symbols without contract addresses → skipped
func (p *Provider) FetchPrices(ctx context.Context, assets []*entity.Asset) ([]entity.StoredPrice, error) {
	if len(assets) == 0 {
		return nil, nil
	}

	// Split assets into two groups.
	type contractAsset struct {
		id      string
		address string
	}
	var contractAssets []contractAsset // ERC-20 with known contract address
	var nativeAssets []*entity.Asset   // native coins looked up by symbol

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
		} else if addr := contractTag(a.Tags); addr != "" {
			contractAssets = append(contractAssets, contractAsset{id: a.ID, address: addr})
		}
		// else: unknown, skip — no reliable CoinGecko mapping
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

	// --- Path 2: ERC-20 by contract address ---
	if len(contractAssets) > 0 {
		addrs := make([]string, len(contractAssets))
		addrToID := make(map[string]string, len(contractAssets))
		for i, ca := range contractAssets {
			addrs[i] = ca.address
			addrToID[strings.ToLower(ca.address)] = ca.id
		}

		pricesByAddr, err := p.client.GetTokenPricesByContract(ctx, cgPlatformEVM, addrs, cgQuoteCurrency)
		if err != nil {
			// Non-fatal: the map may still hold prices from succeeded batches,
			// and the native lookup above has already been collected.
			p.log.Warn("coingecko contract lookup partially failed", "error", err)
		}
		for addr, pd := range pricesByAddr {
			assetID, ok := addrToID[strings.ToLower(addr)]
			if !ok {
				continue
			}
			result = append(result, storedPrice(assetID, pd, now))
		}
	}

	return result, nil
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
