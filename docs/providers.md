# Providers and account configuration

What can be connected, what each connection needs, and what the values mean.

An account is the only way a provider reaches the system: the credentials
resolver looks up syncers by the `provider` slug on accounts carrying the right
capability. A provider with no account registered is invisible, **even when it
needs no key** — the account is the registration, not just the secret store.

> The registry lives in `internal/provider`: factories and the description of
> what each needs are written side by side there, and a test fails the build if
> one exists without the other. This document is maintained by hand against it,
> and `TestProvidersAreDocumented` reads the same catalogue — an adapter with no
> row here fails the build rather than becoming a capability nobody can find.
>
> The catalogue is also served: `PortfolioService.ListProviders` returns the
> slugs, chains, plans and extra fields a client needs to build an account form,
> so a UI offering a choice reads the registry instead of carrying a copy of it.
> It describes credentials and never returns one.

## Account types and what they hold

| Type | Purpose | Capabilities it may grant |
|---|---|---|
| `wallet` | An on-chain address whose balances are synced | `portfolio_sync` |
| `exchange` | A centralized exchange account | `portfolio_sync`, `trading`, `market_data` |
| `broker` | A broker account | `portfolio_sync`, `trading`, `market_data` |
| `bank` | A bank account | `portfolio_sync` |
| `service` | Pure API credentials, holds no positions of its own | `market_data`, `onchain_lookup` |
| `manual` | Positions entered by hand or imported | `manual_positions` |

The type restricts the capabilities: a `wallet` cannot grant `onchain_lookup`,
a `service` cannot hold positions. The matrix is enforced in
`entity.ValidateCapabilities`.

### System scopes

`market_data` and `onchain_lookup` are user-agnostic — a token price or a chain
balance does not belong to whoever owns the key — so an admin may mark them
system-wide via `system_scopes`, letting one account serve every user.
`portfolio_sync` and `trading` always stay personal.

Two things regularly trip this up:

- Every `UpdateAccount` carries an `update_mask` naming the fields to write —
  the mask is required, and an update whose mask omits `systemScopes` leaves the
  field untouched without complaining. Changing it needs the mask to name
  `systemScopes` (camelCase in JSON) *and* an admin caller.
- When two accounts share a provider slug, the resolver takes the **first by
  id**, and ids are UUIDv7 — so the oldest one wins. A key added to the newer
  duplicate is silently ignored while an empty older account shadows it.

## On-chain providers (`service` + `onchain_lookup`)

These sync wallet balances. Every one of them needs an account, key or not.

| `provider` | Chains | Key | Where to get it |
|---|---|---|---|
| `alchemy` | arbitrum, avalanche, base, bsc, eth, linea, optimism, polygon, scroll, zksync | required | alchemy.com, free tier. One request carries the native coin and every ERC-20 for up to five networks, so a ten-chain wallet costs two calls rather than twenty-two. **No fantom**: Alchemy deprecated it, and an account that needs it has to name another provider — a chain claimed but unread would put its holdings out of the sum with nothing saying why. Reports no spam or contract-verification bits, so those signals are absent rather than false |
| `moralis` | arbitrum, avalanche, base, bsc, eth, fantom, linea, optimism, polygon, scroll, zksync | required, **paid** | moralis.io. The free tier ended 2026-09-01: existing keys keep working only under a paid plan, and an unpaid account answers 401 to everything. Reports `possible_spam` and `verified_contract`, which the identity scorer uses and no other EVM source here provides |
| `subscan` | polkadot, kusama, assethub-polkadot, assethub-kusama, hydration, astar, moonbeam | required | subscan.io — unauthenticated access is disabled entirely |
| `tonapi` | ton | optional | tonconsole.com; anonymous calls are rate-limited |
| `helius` | solana | required in practice | dashboard.helius.dev, free tier. Without it the client falls back to the public RPC, which throttles hard and serves no token metadata |
| `esplora` | bitcoin | none | — |
| `cosmos` | cosmos, akash, osmosis | none | — |
| `tzkt` | tezos | none | — |
| `blockchair` | dash, dogecoin | optional, effectively required | blockchair.com/api. The free tier answers `430 IP temporarily blacklisted` from shared networks |

### Two providers for one ecosystem

A wallet account holds an address and never names its reader; the reader comes
from a **credential account** carrying `onchain_lookup`. With both `alchemy` and
`moralis` accounts present, both match an EVM address and **which one serves is
whichever the store returns first** — there is no health check and no
preference, so a provider whose plan has lapsed keeps being chosen and keeps
failing.

Switching therefore means retiring the old account, not just adding the new one:
delete it, or take `onchain_lookup` off it. Leaving both is how "I configured
the new provider" stops meaning "the new provider is used" (`personal-1y6i`).

### Accepted `data` fields

| Key | Applies to | Meaning |
|---|---|---|
| `provider` | all | The slug from the table above. Nothing resolves without it |
| `api_key` | alchemy, moralis, subscan, tonapi, helius, blockchair | Write-only; responses return it masked as `••••` plus the last four characters. Echoing the mask back preserves the stored value |
Endpoints are not configurable per account. `accounts.data` is user-supplied,
and a client whose base URL came from it would make the server issue requests
to whatever address its caller named — cloud metadata, loopback, anything
inside the trust boundary — with the response leaking back through sync error
strings. Pointing an adapter at a self-hosted instance belongs in operator
config, not in user data.

## Market data providers (`service`, `exchange` or `broker` + `market_data`)

| `provider` | Key | `data` fields | Notes |
|---|---|---|---|
| `coingecko` | optional | `api_key`, `tier`, `pro` | The keyless tier accepts exactly one contract address per request and about 30 requests a minute. A free demo key raises this to batches of 30 and allows 10 000 calls a month. Name a paid plan with `tier: pro` — one name picks the API host, the auth header and the allowance, which have to agree. `pro: "true"` is the old spelling of the same thing and still works |
| `binance` | required | `api_key`, `api_secret` | Also serves as an exchange syncer. Prices assets on the global `crypto` market, and only those **bound to a listed pair** in `asset_external_refs` (source `binance`, ref e.g. `BTCUSDT`). The binding is made by discovery during the sweep, and only when exactly one asset in the catalogue claims that pair: Binance is never ambiguous on its own side, so the contest is always between two of *our* assets, and a token minted with a famous ticker would otherwise be handed the real coin's price. A contested pair binds nobody — both claimants stay unpriced and are named in `ValuationCoverage` — and it is resolved by quarantining the impostor, not by the adapter picking a winner. Each row carries the pair's **24h turnover in the quote asset** (`quoteVolume` from `/api/v3/ticker/24hr`), which is what ADR-009 measures a market by; a pair found trading that reports no turnover is published with an explicit zero, so a book nobody touched stays out of the total while the price stays in the catalogue |
| `moex` | none | — | MOEX ISS. Prices assets whose `market` is `moex`: shares and exchange-traded funds from the ISS `shares` market, bonds from `bonds`. Quotes are in roubles, so the RUB/USD rate from `cbr` is what carries them into a USD total. Where a security trades on several boards, the one with the most turnover today wins; outside trading hours the previous close is used and dated to its own session, with no turnover attached. A bond quote is a percentage of nominal and is multiplied out; accrued interest is not included (`personal-b7l`). **Delisted securities get no price at all** — `is_traded=0` means ISS reports no current market, and a last trade from years ago is not a valuation |
| `tinvest` | required | `api_key` | T-Invest broker API, account type `broker`. Prices assets whose `market` is `spbex` or `moex` — the SPB Exchange has no open endpoint of its own, and MOEX arrives as a second opinion alongside `moex` above. **Needs an operator-supplied trust anchor** (`tinvest.rootCAFile`): the API host's certificate chain terminates in a root no standard trust store carries, and without it every request fails inside TLS. See below. Identity is the FIGI, bound automatically into `asset_external_refs` when a ticker resolves to exactly one instrument on the venue — an ambiguous ticker is left unbound and unpriced rather than guessed. Each row carries the instrument's own currency, so a dollar-quoted foreign share and a rouble-quoted domestic one come out of one response correctly. An instrument that is not trading right now — halted, blocked, delisted — is published with an explicit **zero turnover**, which keeps it out of the total under ADR-009 while the number stays in the catalogue: the API reports no volume at all, and without that a frozen 2022 print would sail into the sum |
| `cbr` | none | — | Bank of Russia daily rates. Registered unconditionally, account or not, because nothing about it is per-user. Quotes every currency in the published set in USD, including the rouble, and prices only assets typed `forex` — a token that copied a currency's ticker gets no rate. Prices carry the set's own business day, not the fetch time: a Friday set read on Sunday is two days old and says so |

### The T-Invest trust anchor

`*.tinkoff.ru` is signed by the Russian Trusted Root CA, which no operating
system, browser or Go distribution ships. Standard verification therefore fails
with `self signed certificate in certificate chain`, and the API answers
normally the moment that root is trusted.

The certificate is **not vendored in this repository**, and that is deliberate.
Which certificate authorities a service accepts is a decision for whoever runs
it, not one a source tree should make on their behalf — particularly a state
authority in a project meant to be self-hosted anywhere.

So an operator who wants this provider supplies it:

```yaml
tinvest:
  rootcafile: /etc/greedy-eye/russian_trusted_root_ca.pem
```

Published at `https://gu-st.ru/content/Other/doc/russian_trusted_root_ca.cer`.
Verify before installing — the same root is presented by the API host itself,
and as measured 2026-08-09 both agree on

```
SHA256 D2:6D:2D:02:31:B7:C3:9F:92:CC:73:85:12:BA:54:10:35:19:E4:40:5D:68:B5:BD:70:3E:97:88:CA:8E:CF:31
```

The anchor is added to a pool for **this client only**: nothing else the service
connects to will accept certificates from that authority.

What happens when it is missing, in three cases that are deliberately different:

- **No path configured, no broker account.** Nothing happens. The service starts
  and runs exactly as it does without this provider, which is the normal state
  on every instance that does not use a Russian broker.
- **No path configured, but a broker account exists.** The account stays where
  its owner put it and simply produces no prices. Each sweep logs one warning
  naming the provider, the account and the missing `tinvest.rootCAFile`. Every
  other provider prices as usual — one unconfigured account is not allowed to
  cost the portfolio its CoinGecko, MOEX and FX prices.
- **A path is configured but unreadable.** The process refuses to start. This is
  a typo in operator config, and the alternative is a TLS handshake error inside
  a sweep hours later with nothing pointing back at the cause.

## Broker accounts (`broker` + `portfolio_sync`)

One account here is ONE account at the broker. A single token reaches several,
and each of them is a separate account in this system: merging them would
collapse two holdings of the same share into one row and make a transfer between
them invisible — the sum does not move, so for anything watching, the event never
happened. `GetAccounts` at the broker is how an operator learns the ids to fill
in; creating the accounts stays a human decision.

| `provider` | `data` fields |
|---|---|
| `tinvest` | `api_key`, `broker_account_id`, plus the trust anchor rules above (`root_ca` on the account, or `base_url` pointing at a plaintext replay host) |

An account naming no `broker_account_id` refuses to sync rather than picking the
first account the token reaches: reading somebody's other portfolio into this one
and reporting success is exactly the silent degradation this field exists to
prevent.

What arrives: shares, funds, bonds and cash, each bound by the broker's own
instrument id (`asset_external_refs`, source `tinvest`) — except cash, which
resolves to the currency and is bound to nothing, because the broker's id for a
currency line names a settlement instrument rather than the money. A bond is
brought in by quantity and is not valued (accrued interest and the bond model are
`personal-b7l`). A position the broker reports as blocked becomes its own
`locked` holding beside the liquid part.

The sync's own report says what it could not name (`positions_skipped`) and what
it placed on a guessed venue (`assets_defaulted_market`); a non-zero skip count
also holds back the removal of positions the snapshot no longer contains, since
the snapshot cannot speak for what it did not read.

## Exchange accounts (`exchange` + `portfolio_sync`)

| `provider` | `data` fields |
|---|---|
| `binance` | `api_key`, `api_secret` |
| `gateio` | `api_key`, `api_secret`, optional `base_url` |

`gateio` reads spot balances only, and is **not** registered as a price source
even though Gate.io serves prices: a balance reader that also quotes becomes a
second author of the total, and a price that arrives beside a balance carries no
date, no market-depth gate and no provenance.

Its `locked` is **disjoint** from `available` — what sits in open orders is held
out of the spendable figure rather than inside it — so a position is the sum of
the two. That is the opposite of every Substrate chain here, where `reserved` is
a subset of `balance` and adding it doubles the largest holding. The direction
cannot be inferred from the field names; both are pinned by tests.

`base_url` is for reaching a regional host. Read-only API keys are enough:
nothing in this path trades.

## Wallet accounts (`wallet` + `portfolio_sync`)

These hold no credentials — the address is public. They name what to sync; the
provider account above supplies how.

| Key | Meaning |
|---|---|
| `address` | A single address |
| `addresses` | Several addresses, comma- or space-separated. All must belong to the same ecosystem: the syncer is resolved from the first and reused. Intended for UTXO chains, where one wallet spans many addresses |
| `chain` | Which chains to sync, comma-separated. Empty or `auto` means auto-discovery |

### Auto-discovery and when to name the chain

With `chain` empty, the provider is chosen from the address's shape. This is
checked structurally — by checksum or decoded length, never by prefix
character — because four of these ecosystems share the base58 alphabet and a
misroute does not fail loudly: the wrong chain reports an address it has never
seen, which reads as an empty wallet and zeroes the position.

Auto-discovery then behaves differently per ecosystem, which is worth knowing
before deciding whether to name the chain:

- **Substrate** sweeps every network. One SS58 key is the same account
  everywhere, and Subscan resolves any network's form of it.
- **Cosmos** sweeps every zone, re-encoding the address to each chain's prefix
  first — unlike Subscan, an LCD accepts only its own.
- **Blockchair** asks exactly one chain, identified from the address's version
  byte. An address is valid on one of these chains by construction.
- **Solana, TON, Tezos, Bitcoin** are single-chain.

Two cases must name the chain explicitly, because their addresses cannot be
recognized:

- **Moonbeam** uses EVM addresses despite being served by `subscan`.
- **Bitcoin accounts using `addresses`** are fine on shape, but naming
  `bitcoin` costs nothing and documents intent.

## Request budget

Every adapter client goes through a shared token bucket keyed by provider and
API key, so all clients built from one credential — the resolver makes a new
one per account, per sync — draw on a single budget. Without this, a sweep over
three accounts on one key sends three times the rate the provider meters, which
is how the Subscan plan limit was tripped in the first place.

The budget has two dimensions, because providers meter both. **Rate** is
requests per second. **Volume** is how many calls a plan allows per month or
per day — the limit that actually bit us: a price sweep stayed well under
CoinGecko's per-second ceiling and still spent its whole free monthly
allowance in eight days.

Defaults live in `internal/adapter/ratelimit` (`defaultLimits`), deliberately
under each provider's published ceiling, with a separate, tighter entry for the
keyless tier where one exists. They are not repeated here: a table in two
places drifts.

### Naming the plan

Which limit applies comes from the account's `data.tier`, next to the key:

```yaml
data:
  provider: coingecko
  api_key: CG-...
  tier: pro        # omit for the provider's free plan; "keyless" without a key
```

Leaving `tier` empty infers it — keyless with no `api_key`, the free keyed plan
with one. Moving to a paid plan is therefore a settings edit, not a release,
and the numbers behind each tier stay in code where they can carry the
reasoning that produced them.

A custom request budget lives on the account too, beside the key it belongs to:

```yaml
data:
  provider: coingecko
  api_key: CG-...
  tier: pro        # omit for the provider's free plan
  rps: "1.2"       # fractional values are meaningful: 0.5 is one request per 2s
  burst: "1"       # keep at 1 for per-second meters; bursts are what trip them
  quota: "2000"    # what THIS deployment may spend
  period: month    # or "day"; required whenever quota is set
```

Every field is optional and an omitted one leaves the plan's own value alone, so
naming a burst does not silently zero the rate. A field that cannot be read is
dropped with a warning rather than refusing to start: this arrives from the
database while the process runs, and a mistyped number in a form must not take
the service down.

### Sharing one plan between deployments

`quota` is the field that matters when one key serves more than one instance.

It used to be forbidden to touch a volume allowance at all, on the grounds that
it is the account's plan and the account's money. That reasoning held while one
key meant one deployment. It stopped holding on 2026-08-11: dev and production
ran the same CoinGecko key, each counted spend into its own database, each
divided the remaining allowance as though it owned all 10,000 calls — and the
plan ran dry with both instances still reporting room, then answered 429 for
four days.

So the setting does not raise or lower the plan. It declares **this
deployment's share** of it, and it lives with the key because nothing else
knows which key is being shared. Set it below the plan on every instance that
shares one; leave it unset and the provider's own plan applies whole, which is
right when the key is not shared.

A `quota` with no `period` is ignored, loudly. It would never roll over — the
counters reset on a boundary that does not exist — so the allowance would be
spent once and that provider would stay silent until someone noticed, surfacing
days later as "prices stopped updating".

Repeated refusals are also self-limiting now: each 429 that no successful
response has interrupted doubles the pause, up to a couple of hours. That
asserts nothing about *why* a provider is refusing — rate and exhausted plan
arrive as the same status — it only stops paying for an answer already given.
One good response resets it. The escalation lives in memory: a restart starts
the run over, which costs one pause rather than a burn, because the spend the
quota check reads is persisted.

Providers not named keep their defaults, and a provider with no default at all
gets 1 rps.

### Spending the volume allowance

Requests carry a class. Unattended work — the price sweep, catalogue
refreshes — stops at 80% of the period's allowance; requests a person is
waiting on may spend the rest. A sweep that empties the month must not also
take the Sync button down with it.

Spend is written to `provider_usage` and restored at startup, keyed by provider
and a fingerprint of the API key (never the key itself). An allowance tracked
only in memory is no allowance: a deploy would hand the process a fresh one
while the provider keeps counting. Counters are added to, not set, so two
backend instances sum the way the provider sums them.

The price sweep logs what each run cost and what the period has cost so far —
`coingecko_requests` and `coingecko_period_requests` on the
`scheduler: prices fetched` line. Multiply the first by the runs in a month and
compare with the plan.

When a provider answers `429`, `418` or Blockchair's `430`, its bucket freezes
for the `Retry-After` it asked for (one minute if it did not say, capped at
fifteen) and traffic resumes on its own. The response still reaches the
adapter, so sync errors keep carrying the provider's own message. When the
volume allowance is gone the request is refused outright instead: no amount of
waiting fixes it before the period rolls over.

The rate bucket is in-process; a second backend instance keeps its own and the
provider sees the sum. Volume is shared through the database.

## Checking that a connection works

Sync the account. A provider that resolved returns balances or an empty list; a
provider that did not returns `Unimplemented: wallet sync not configured` —
which is the signal that the `service` account is missing, has the wrong slug,
or lacks `onchain_lookup`.

Deprecated environment-variable credentials still work as a fallback and log a
warning once per process per provider. Anything still relying on them should be
migrated to an account.
