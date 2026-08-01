# Providers and account configuration

What can be connected, what each connection needs, and what the values mean.

An account is the only way a provider reaches the system: the credentials
resolver looks up syncers by the `provider` slug on accounts carrying the right
capability. A provider with no account registered is invisible, **even when it
needs no key** — the account is the registration, not just the secret store.

> This document is maintained by hand against the registry in
> `cmd/eye/main.go`. Adding an adapter without a row here leaves a capability
> nobody can discover.

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

- Changing `system_scopes` needs an explicit `update_mask` naming
  `systemScopes` (camelCase in JSON) and an admin caller. A plain update leaves
  the field untouched without complaining.
- When two accounts share a provider slug, the resolver takes the **first by
  id**, and ids are UUIDv7 — so the oldest one wins. A key added to the newer
  duplicate is silently ignored while an empty older account shadows it.

## On-chain providers (`service` + `onchain_lookup`)

These sync wallet balances. Every one of them needs an account, key or not.

| `provider` | Chains | Key | Where to get it |
|---|---|---|---|
| `moralis` | 8 EVM chains | required | moralis.io |
| `subscan` | polkadot, kusama, assethub-polkadot, assethub-kusama, hydration, astar, moonbeam | required | subscan.io — unauthenticated access is disabled entirely |
| `tonapi` | ton | optional | tonconsole.com; anonymous calls are rate-limited |
| `helius` | solana | required in practice | dashboard.helius.dev, free tier. Without it the client falls back to the public RPC, which throttles hard and serves no token metadata |
| `esplora` | bitcoin | none | — |
| `cosmos` | cosmos, akash, osmosis | none | — |
| `tzkt` | tezos | none | — |
| `blockchair` | dash, dogecoin | optional, effectively required | blockchair.com/api. The free tier answers `430 IP temporarily blacklisted` from shared networks |

### Accepted `data` fields

| Key | Applies to | Meaning |
|---|---|---|
| `provider` | all | The slug from the table above. Nothing resolves without it |
| `api_key` | moralis, subscan, tonapi, helius, blockchair | Write-only; responses return it masked as `••••` plus the last four characters. Echoing the mask back preserves the stored value |
Endpoints are not configurable per account. `accounts.data` is user-supplied,
and a client whose base URL came from it would make the server issue requests
to whatever address its caller named — cloud metadata, loopback, anything
inside the trust boundary — with the response leaking back through sync error
strings. Pointing an adapter at a self-hosted instance belongs in operator
config, not in user data.

## Market data providers (`service` or `exchange` + `market_data`)

| `provider` | Key | `data` fields | Notes |
|---|---|---|---|
| `coingecko` | optional | `api_key`, `tier`, `pro` | The keyless tier accepts exactly one contract address per request and about 30 requests a minute. A free demo key raises this to batches of 30 and allows 10 000 calls a month. Name a paid plan with `tier: pro` — it changes the API host and the allowance. `pro: "true"` is the old spelling of the same thing and still works |
| `binance` | required | `api_key`, `api_secret` | Also serves as an exchange syncer |

## Exchange accounts (`exchange` + `portfolio_sync`)

| `provider` | `data` fields |
|---|---|
| `binance` | `api_key`, `api_secret` |

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

To throttle a provider deployment-wide, without a rebuild — after an
enforcement notice, say:

```yaml
ratelimit:
  subscan:
    rps: 1.8   # fractional values are meaningful: 0.5 is one request per 2s
    burst: 1   # keep at 1 for per-second meters; bursts are what trip them
```

This applies to **every credential** on that provider, a user's own account
included. That is deliberate: providers meter rate per IP as much as per key,
and the whole process shares one address, so a rate one account is told to
respect is a rate all of them have to respect.

It changes rate only. A **volume allowance stays with the key it belongs to** —
it is that account's plan and that account's money, and no operator setting can
raise or lower it. Fields left unset keep the tier's value, so naming a burst
does not silently zero the rate.

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
