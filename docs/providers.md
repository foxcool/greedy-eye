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
| `base_url` | esplora, cosmos, tzkt, blockchair | Overrides the default endpoint — a self-hosted node, or Blockstream instead of mempool.space. Leave empty for the default |

## Market data providers (`service` or `exchange` + `market_data`)

| `provider` | Key | `data` fields | Notes |
|---|---|---|---|
| `coingecko` | optional | `api_key`, `pro` | The keyless tier accepts exactly one contract address per request and about 30 requests a minute. A free demo key raises this to batches of 30. Set `pro` to `true` only for a paid plan — it changes the API host |
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

## Checking that a connection works

Sync the account. A provider that resolved returns balances or an empty list; a
provider that did not returns `Unimplemented: wallet sync not configured` —
which is the signal that the `service` account is missing, has the wrong slug,
or lacks `onchain_lookup`.

Deprecated environment-variable credentials still work as a fallback and log a
warning once per process per provider. Anything still relying on them should be
migrated to an account.
