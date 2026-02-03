schema "public" {}

table "users" {
  schema = schema.public

  column "id" {
    type = bigint
    null = false
    identity {}
  }
  column "uuid" {
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

table "accounts" {
  schema = schema.public

  column "id" {
    type = bigint
    null = false
    identity {}
  }
  column "uuid" {
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
  column "user_id" {
    type = bigint
    null = false
  }

  primary_key {
    columns = [column.id]
  }

  foreign_key "accounts_users_accounts" {
    columns     = [column.user_id]
    ref_columns = [table.users.column.id]
    on_update   = NO_ACTION
    on_delete   = NO_ACTION
  }
}

table "assets" {
  schema = schema.public

  column "id" {
    type = bigint
    null = false
    identity {}
  }
  column "uuid" {
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
  column "tags" {
    type = jsonb
    null = false
  }

  primary_key {
    columns = [column.id]
  }

  index "asset_tags" {
    columns = [column.tags]
    type    = GIN
  }
}

table "portfolios" {
  schema = schema.public

  column "id" {
    type = bigint
    null = false
    identity {}
  }
  column "uuid" {
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
    type = bigint
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
    type = bigint
    null = false
    identity {}
  }
  column "uuid" {
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
    type = bigint
    null = false
  }
  column "decimals" {
    type = bigint
    null = false
  }
  column "account_id" {
    type = bigint
    null = false
  }
  column "asset_id" {
    type = bigint
    null = false
  }
  column "portfolio_id" {
    type = bigint
    null = true
  }

  primary_key {
    columns = [column.id]
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
    type = bigint
    null = false
    identity {}
  }
  column "uuid" {
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
    type = bigint
    null = false
  }
  column "open" {
    type = bigint
    null = true
  }
  column "high" {
    type = bigint
    null = true
  }
  column "low" {
    type = bigint
    null = true
  }
  column "close" {
    type = bigint
    null = true
  }
  column "volume" {
    type = bigint
    null = true
  }
  column "timestamp" {
    type = timestamptz
    null = false
  }
  column "asset_id" {
    type = bigint
    null = false
  }
  column "base_asset_id" {
    type = bigint
    null = false
  }

  primary_key {
    columns = [column.id]
  }

  index "price_asset_id_timestamp" {
    columns = [column.asset_id, column.timestamp]
    unique  = true
  }

  foreign_key "prices_assets_prices" {
    columns     = [column.asset_id]
    ref_columns = [table.assets.column.id]
    on_update   = NO_ACTION
    on_delete   = NO_ACTION
  }

  foreign_key "prices_assets_prices_base" {
    columns     = [column.base_asset_id]
    ref_columns = [table.assets.column.id]
    on_update   = NO_ACTION
    on_delete   = NO_ACTION
  }
}

table "transactions" {
  schema = schema.public

  column "id" {
    type = bigint
    null = false
    identity {}
  }
  column "uuid" {
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
    type = bigint
    null = false
  }
  column "asset_transactions" {
    type = bigint
    null = true
  }

  primary_key {
    columns = [column.id]
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
