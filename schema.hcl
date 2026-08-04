schema "public" {}

table "users" {
  schema = schema.public

  column "id" {
    type = uuid
    null = false
  }
  column "email" {
    type = character_varying
    null = false
  }
  column "name" {
    type = character_varying
    null = false
  }
  column "preferences" {
    type = jsonb
    null = false
  }
  column "created_at" {
    type = timestamptz
    null = false
  }
  column "updated_at" {
    type = timestamptz
    null = false
  }

  primary_key {
    columns = [column.id]
  }

  index "users_email_key" {
    columns = [column.email]
    unique  = true
  }
}

// user_settings is a generic per-user key-value store, not a dashboard table.
// The first consumer is the dashboard layout ("dashboard.v1"), but appearance
// and other cross-device preferences land here too — a table per preference
// would mean a migration per preference. users.preferences already exists and
// is deliberately left alone: it is a single blob rewritten as a whole, which
// makes two independent settings a lost-update race against each other.
table "user_settings" {
  schema = schema.public

  column "user_id" {
    type = uuid
    null = false
  }
  // key names the setting and its schema version ("dashboard.v1"). Versioning
  // in the key rather than inside the value means an incompatible layout is a
  // new row the old client never reads, instead of a value it misparses.
  column "key" {
    type = character_varying
    null = false
  }
  column "value" {
    type = jsonb
    null = false
  }
  column "updated_at" {
    type = timestamptz
    null = false
  }

  primary_key {
    columns = [column.user_id, column.key]
  }

  foreign_key "user_settings_users" {
    columns     = [column.user_id]
    ref_columns = [table.users.column.id]
    on_update   = NO_ACTION
    on_delete   = CASCADE
  }
}

table "accounts" {
  schema = schema.public

  column "id" {
    type = uuid
    null = false
  }
  column "created_at" {
    type = timestamptz
    null = false
  }
  column "updated_at" {
    type = timestamptz
    null = false
  }
  column "name" {
    type = character_varying
    null = false
  }
  column "description" {
    type = character_varying
    null = true
  }
  column "type" {
    type = character_varying
    null = false
  }
  column "data" {
    type = jsonb
    null = false
  }
  # Capabilities the account credentials allow (portfolio_sync, trading,
  # market_data, onchain_lookup); JSON array of strings.
  column "capabilities" {
    type    = jsonb
    null    = false
    default = sql("'[]'")
  }
  # Subset of capabilities shared system-wide (usable for any user's operations);
  # admin-managed, only user-agnostic capabilities allowed.
  column "system_scopes" {
    type    = jsonb
    null    = false
    default = sql("'[]'")
  }
  column "user_id" {
    type = uuid
    null = false
  }
  column "portfolio_id" {
    type = uuid
    null = true
  }

  primary_key {
    columns = [column.id]
  }

  index "account_system_scopes" {
    columns = [column.system_scopes]
    type    = GIN
  }

  foreign_key "accounts_users_accounts" {
    columns     = [column.user_id]
    ref_columns = [table.users.column.id]
    on_update   = NO_ACTION
    on_delete   = NO_ACTION
  }
  foreign_key "accounts_portfolios_account" {
    columns     = [column.portfolio_id]
    ref_columns = [table.portfolios.column.id]
    on_update   = NO_ACTION
    on_delete   = SET_NULL
  }
}

table "assets" {
  schema = schema.public

  column "id" {
    type = uuid
    null = false
  }
  column "created_at" {
    type = timestamptz
    null = false
  }
  column "updated_at" {
    type = timestamptz
    null = false
  }
  column "symbol" {
    type = character_varying
    null = false
  }
  column "name" {
    type = character_varying
    null = false
  }
  column "type" {
    type = character_varying
    null = false
  }
  // Listing market of the instrument (crypto, nasdaq, moex), not the price source.
  // Crypto is a single global market; the default backfills existing rows.
  column "market" {
    type    = character_varying
    null    = false
    default = "crypto"
  }
  // Quote currency/base where applicable (e.g. USD for a NASDAQ listing).
  column "quote" {
    type = character_varying
    null = true
  }
  column "tags" {
    type = jsonb
    null = false
  }
  // Identity axis (scam-filtering): whether the asset is what it claims to be.
  // A permanent property, distinct from a real asset's situational risk
  // (asset_risk_flags) and from a user's per-holding excluded decision.
  // Values: unknown | legit | suspect | scam | impersonation.
  column "identity_verdict" {
    type    = character_varying
    null    = false
    default = "unknown"
  }
  // Last score (0..1) behind an automated verdict; null for user verdicts.
  column "identity_score" {
    type = numeric
    null = true
  }
  // {signal: weight} that fired, for UI explainability. Null until first scored.
  column "identity_signals" {
    type = jsonb
    null = true
  }
  // Provenance of the verdict: 'heuristic' | 'provider:<name>' | 'curated' |
  // 'user:<id>'. A user verdict is terminal — rescoring never overwrites it.
  column "verdict_source" {
    type = character_varying
    null = true
  }
  column "verdict_set_at" {
    type = timestamptz
    null = true
  }

  primary_key {
    columns = [column.id]
  }

  index "asset_symbol" {
    columns = [column.symbol]
  }

  index "asset_symbol_market_type" {
    columns = [column.symbol, column.market, column.type]
    unique  = true
  }

  index "asset_tags" {
    columns = [column.tags]
    type    = GIN
  }
}

// Maps an asset to its identifier in an external namespace: an on-chain
// contract/mint, a provider coin id (coingecko), a broker FIGI. Identity on a
// chain is the contract, not the symbol — this table lets sync resolve a token
// by its contract so a scam clone of a real ticker cannot merge into the real
// asset (personal-avm, personal-iyr). source namespaces the ref: "onchain:<chain>"
// for a contract/mint, "coingecko"/"cmc" for a provider id. UNIQUE(source, ref)
// makes the same address on two chains two distinct identities.
table "asset_external_refs" {
  schema = schema.public

  column "id" {
    type = uuid
    null = false
  }
  column "created_at" {
    type = timestamptz
    null = false
  }
  column "asset_id" {
    type = uuid
    null = false
  }
  // Namespace of the ref: "onchain:<chain>" (contract/mint), "coingecko", "cmc",
  // broker id spaces.
  column "source" {
    type = character_varying
    null = false
  }
  // The external identifier within source (contract address, mint, coin id).
  column "ref" {
    type = character_varying
    null = false
  }
  // How the mapping was made: auto | manual | seed. A manual link is terminal —
  // auto-discovery never overwrites it.
  column "origin" {
    type    = character_varying
    null    = false
    default = "auto"
  }

  primary_key {
    columns = [column.id]
  }

  index "asset_external_ref_source_ref" {
    columns = [column.source, column.ref]
    unique  = true
  }

  index "asset_external_ref_asset_id" {
    columns = [column.asset_id]
  }

  foreign_key "asset_external_refs_assets" {
    columns     = [column.asset_id]
    ref_columns = [table.assets.column.id]
    on_update   = NO_ACTION
    on_delete   = CASCADE
  }
}

// Situational risk of a real asset (axis 2): exploit, depeg, frozen transfers,
// deprecation, delisting, sanctions freeze. Temporary and about real value, so
// unlike an identity verdict it does NOT exclude the asset from sums — the money
// is real and action may be needed on it. Every flag carries a review_at so a
// hack flag does not hang forever. Venue/account-level risk lives elsewhere
// (personal-ejr); this table is per-asset.
table "asset_risk_flags" {
  schema = schema.public

  column "id" {
    type = uuid
    null = false
  }
  column "created_at" {
    type = timestamptz
    null = false
  }
  column "asset_id" {
    type = uuid
    null = false
  }
  // Risk kind: exploit | depeg | frozen_transfers | deprecation | delisting |
  // sanctions_freeze.
  column "kind" {
    type = character_varying
    null = false
  }
  column "note" {
    type = character_varying
    null = true
  }
  // Derived action direction (axis 3): none | hold | exit_soon.
  column "action_hint" {
    type = character_varying
    null = true
  }
  // Review or end date; required so a temporary flag is never permanent.
  column "review_at" {
    type = timestamptz
    null = true
  }
  column "set_by" {
    type = character_varying
    null = true
  }

  primary_key {
    columns = [column.id]
  }

  index "asset_risk_flag_asset_id" {
    columns = [column.asset_id]
  }

  foreign_key "asset_risk_flags_assets" {
    columns     = [column.asset_id]
    ref_columns = [table.assets.column.id]
    on_update   = NO_ACTION
    on_delete   = CASCADE
  }
}

table "portfolios" {
  schema = schema.public

  column "id" {
    type = uuid
    null = false
  }
  column "created_at" {
    type = timestamptz
    null = false
  }
  column "updated_at" {
    type = timestamptz
    null = false
  }
  column "name" {
    type = character_varying
    null = false
  }
  column "description" {
    type = character_varying
    null = true
  }
  column "data" {
    type = jsonb
    null = false
  }
  column "user_id" {
    type = uuid
    null = false
  }

  primary_key {
    columns = [column.id]
  }

  foreign_key "portfolios_users_portfolios" {
    columns     = [column.user_id]
    ref_columns = [table.users.column.id]
    on_update   = NO_ACTION
    on_delete   = NO_ACTION
  }
}

table "holdings" {
  schema = schema.public

  column "id" {
    type = uuid
    null = false
  }
  column "created_at" {
    type = timestamptz
    null = false
  }
  column "updated_at" {
    type = timestamptz
    null = false
  }
  column "amount" {
    # NUMERIC (arbitrary precision) holds raw uint256 token balances that overflow bigint.
    type = numeric
    null = false
  }
  column "decimals" {
    type = bigint
    null = false
  }
  column "account_id" {
    type = uuid
    null = false
  }
  column "asset_id" {
    type = uuid
    null = false
  }
  column "portfolio_id" {
    type = uuid
    null = true
  }
  column "excluded" {
    type    = boolean
    null    = false
    default = false
  }
  column "source" {
    # Creation provenance: sync | manual | llm_import. Default backfills pre-provenance rows as sync.
    type    = character_varying
    null    = false
    default = "sync"
  }
  # Network this amount sits on ("eth", "base", "solana"). Empty means the
  # position is not chain-scoped (an exchange balance, a manual entry) or
  # predates the column — not "Ethereum". It belongs on the row and not on the
  # asset: the same USDC on Base and on Arbitrum is one asset in two places,
  # and summing the two into one row is what made "how much on which chain"
  # unanswerable.
  column "chain" {
    type    = character_varying
    null    = false
    default = ""
  }
  # How soon the amount can be spent: liquid | staked | unbonding | locked |
  # vesting. Empty means the source could not partition the balance — it is NOT
  # a synonym for liquid, and a runway figure must not read it as one.
  column "liquidity" {
    type    = character_varying
    null    = false
    default = ""
  }
  column "import_id" {
    type = uuid
    null = true
  }

  primary_key {
    columns = [column.id]
  }

  index "holding_import_id" {
    columns = [column.import_id]
    where   = "import_id IS NOT NULL"
  }

  # Sync keys positions by (account, asset, chain, liquidity). No unique constraint is
  # declared: the table has never had one, manual and synced rows for the same
  # asset legitimately coexist, and adding one now would fail an apply on any
  # instance already carrying such a pair.
  index "holding_account_asset_chain_liquidity" {
    columns = [column.account_id, column.asset_id, column.chain, column.liquidity]
  }

  foreign_key "holdings_accounts_holdings" {
    columns     = [column.account_id]
    ref_columns = [table.accounts.column.id]
    on_update   = NO_ACTION
    on_delete   = NO_ACTION
  }

  foreign_key "holdings_assets_holdings" {
    columns     = [column.asset_id]
    ref_columns = [table.assets.column.id]
    on_update   = NO_ACTION
    on_delete   = NO_ACTION
  }

  foreign_key "holdings_portfolios_holdings" {
    columns     = [column.portfolio_id]
    ref_columns = [table.portfolios.column.id]
    on_update   = NO_ACTION
    on_delete   = SET_NULL
  }
}

table "prices" {
  schema = schema.public

  column "id" {
    type = uuid
    null = false
  }
  column "source_id" {
    type = character_varying
    null = false
  }
  column "interval" {
    type = character_varying
    null = false
  }
  column "decimals" {
    type = bigint
    null = false
  }
  column "last" {
    # NUMERIC: raw integer price scaled by decimals; arbitrary precision, no overflow.
    type = numeric
    null = false
  }
  column "open" {
    type = numeric
    null = true
  }
  column "high" {
    type = numeric
    null = true
  }
  column "low" {
    type = numeric
    null = true
  }
  column "close" {
    type = numeric
    null = true
  }
  column "volume" {
    type = numeric
    null = true
  }
  column "market_cap" {
    # Market context of this quote, scaled by decimals like every other amount
    # in the row. NULL means the source reported none — not a cap of zero.
    type = numeric
    null = true
  }
  column "timestamp" {
    type = timestamptz
    null = false
  }
  column "asset_id" {
    type = uuid
    null = false
  }
  column "base_asset_id" {
    type = uuid
    null = false
  }

  primary_key {
    columns = [column.id]
  }

  // Uniqueness is per (asset, source, instant, pair). The old (asset_id,
  // timestamp) index made two providers pricing the same asset at the same
  // moment a constraint violation, which CreatePrices then counted as a failed
  // fetch. Column order is load-bearing: the (asset_id, source_id, timestamp)
  // prefix is what GetLatestPrice and the staleness lookup scan, so both get an
  // ordered index seek instead of a filter over every price row of the asset.
  index "price_asset_source_timestamp_base" {
    columns = [column.asset_id, column.source_id, column.timestamp, column.base_asset_id]
    unique  = true
  }

  foreign_key "prices_assets_asset" {
    columns     = [column.asset_id]
    ref_columns = [table.assets.column.id]
    on_update   = NO_ACTION
    on_delete   = NO_ACTION
  }

  foreign_key "prices_assets_base_asset" {
    columns     = [column.base_asset_id]
    ref_columns = [table.assets.column.id]
    on_update   = NO_ACTION
    on_delete   = RESTRICT
  }
}

// How much of a provider credential's plan has been spent in the current
// period. A monthly allowance tracked only in memory is no allowance at all: a
// deploy would hand the process a fresh one while the provider keeps counting.
//
// The key is a fingerprint, never the API key: this table is dumped, backed up
// and read by humans, and the secret has no business being in any of those.
// Counters are added to, not set, so two backend instances sum the way the
// provider sums them.
table "provider_usage" {
  schema = schema.public

  column "provider" {
    type = character_varying
    null = false
  }
  // SHA-256 prefix of the API key, or "keyless".
  column "key_fingerprint" {
    type = character_varying
    null = false
  }
  // Start of the plan's billing window, UTC — providers meter on calendar
  // boundaries, not on a rolling window.
  column "period_start" {
    type = timestamptz
    null = false
  }
  column "requests" {
    type    = bigint
    null    = false
    default = 0
  }
  // Times the provider answered "slow down" (429/418/430). A rising count is
  // the signal that the rate limit, not the volume limit, needs attention.
  column "backoffs" {
    type    = bigint
    null    = false
    default = 0
  }
  column "updated_at" {
    type = timestamptz
    null = false
  }

  primary_key {
    columns = [column.provider, column.key_fingerprint, column.period_start]
  }
}

// Per (asset, source) record of price fetch attempts, which is what an
// unattended sweep selects on. Freshness cannot be read off the prices table
// alone: an asset the provider does not list never gets a price, so an
// oldest-first rotation keyed on price timestamps would put it at the head of
// the queue forever and spend the whole per-sweep budget re-asking for it.
//
// next_attempt_at is a materialized deadline rather than a computed one: the
// exponential push-out is applied on write, so the selection stays an index
// seek. misses drives that exponent and resets on the first success.
table "price_fetch_attempts" {
  schema = schema.public

  column "asset_id" {
    type = uuid
    null = false
  }
  // Provider slug, matching prices.source_id ("coingecko", "binance").
  column "source_id" {
    type = character_varying
    null = false
  }
  column "attempted_at" {
    type = timestamptz
    null = false
  }
  // Last attempt that actually returned a price. NULL means the provider has
  // never priced this asset.
  column "succeeded_at" {
    type = timestamptz
    null = true
  }
  // Consecutive attempts that returned nothing.
  column "misses" {
    type    = integer
    null    = false
    default = 0
  }
  column "next_attempt_at" {
    type = timestamptz
    null = false
  }

  primary_key {
    columns = [column.asset_id, column.source_id]
  }

  // Serves the due-target lookup: one source, deadline in the past, oldest first.
  index "price_fetch_attempt_due" {
    columns = [column.source_id, column.next_attempt_at]
  }

  foreign_key "price_fetch_attempts_assets" {
    columns     = [column.asset_id]
    ref_columns = [table.assets.column.id]
    on_update   = NO_ACTION
    on_delete   = CASCADE
  }
}

table "transactions" {
  schema = schema.public

  column "id" {
    type = uuid
    null = false
  }
  column "type" {
    type = character_varying
    null = false
  }
  column "status" {
    type = character_varying
    null = false
  }
  column "data" {
    type = jsonb
    null = false
  }
  column "created_at" {
    type = timestamptz
    null = false
  }
  column "updated_at" {
    type = timestamptz
    null = false
  }
  column "account_id" {
    type = uuid
    null = false
  }
  column "asset_transactions" {
    type = uuid
    null = true
  }
  column "source" {
    # Creation provenance: sync | manual | llm_import. Default backfills pre-provenance rows as sync.
    type    = character_varying
    null    = false
    default = "sync"
  }
  column "import_id" {
    type = uuid
    null = true
  }

  primary_key {
    columns = [column.id]
  }

  index "transaction_import_id" {
    columns = [column.import_id]
    where   = "import_id IS NOT NULL"
  }

  foreign_key "transactions_accounts_transactions" {
    columns     = [column.account_id]
    ref_columns = [table.accounts.column.id]
    on_update   = NO_ACTION
    on_delete   = NO_ACTION
  }

  foreign_key "transactions_assets_transactions" {
    columns     = [column.asset_transactions]
    ref_columns = [table.assets.column.id]
    on_update   = NO_ACTION
    on_delete   = SET_NULL
  }
}

table "rules" {
  schema = schema.public

  column "id" {
    type = uuid
    null = false
  }
  column "user_id" {
    type = uuid
    null = false
  }
  column "portfolio_id" {
    type = uuid
    null = false
  }
  column "name" {
    type = character_varying
    null = false
  }
  column "description" {
    type = character_varying
    null = true
  }
  column "rule_type" {
    type = character_varying
    null = false
  }
  column "status" {
    type = character_varying
    null = false
  }
  column "configuration" {
    type = jsonb
    null = false
  }
  column "schedule" {
    type = jsonb
    null = true
  }
  column "created_at" {
    type = timestamptz
    null = false
  }
  column "updated_at" {
    type = timestamptz
    null = false
  }

  primary_key {
    columns = [column.id]
  }

  foreign_key "rules_users_rules" {
    columns     = [column.user_id]
    ref_columns = [table.users.column.id]
    on_update   = NO_ACTION
    on_delete   = NO_ACTION
  }

  foreign_key "rules_portfolios_rules" {
    columns     = [column.portfolio_id]
    ref_columns = [table.portfolios.column.id]
    on_update   = NO_ACTION
    on_delete   = NO_ACTION
  }

  index "rules_user_id" {
    columns = [column.user_id]
  }

  index "rules_portfolio_id" {
    columns = [column.portfolio_id]
  }

  index "rules_status" {
    columns = [column.status]
  }
}

table "rule_executions" {
  schema = schema.public

  column "id" {
    type = uuid
    null = false
  }
  column "rule_id" {
    type = uuid
    null = false
  }
  column "portfolio_id" {
    type = uuid
    null = true
  }
  column "user_id" {
    type = uuid
    null = true
  }
  column "status" {
    type = character_varying
    null = false
  }
  column "error_message" {
    type = character_varying
    null = true
  }
  column "created_transaction_ids" {
    type = jsonb
    null = false
  }
  column "affected_holding_ids" {
    type = jsonb
    null = false
  }
  column "transactions_created" {
    type    = bigint
    null    = false
    default = 0
  }
  column "execution_summary" {
    type = jsonb
    null = true
  }
  column "started_at" {
    type = timestamptz
    null = false
  }
  column "completed_at" {
    type = timestamptz
    null = true
  }

  primary_key {
    columns = [column.id]
  }

  foreign_key "rule_executions_rules" {
    columns     = [column.rule_id]
    ref_columns = [table.rules.column.id]
    on_update   = NO_ACTION
    on_delete   = NO_ACTION
  }

  foreign_key "rule_executions_portfolios" {
    columns     = [column.portfolio_id]
    ref_columns = [table.portfolios.column.id]
    on_update   = NO_ACTION
    on_delete   = SET_NULL
  }

  foreign_key "rule_executions_users" {
    columns     = [column.user_id]
    ref_columns = [table.users.column.id]
    on_update   = NO_ACTION
    on_delete   = SET_NULL
  }

  index "rule_executions_rule_id" {
    columns = [column.rule_id]
  }

  index "rule_executions_user_id" {
    columns = [column.user_id]
  }

  index "rule_executions_status" {
    columns = [column.status]
  }

  index "rule_executions_started_at" {
    columns = [column.started_at]
  }
}
