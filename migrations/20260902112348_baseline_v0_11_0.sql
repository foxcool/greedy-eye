-- Create "provider_usage" table
CREATE TABLE "provider_usage" (
  "provider" character varying NOT NULL,
  "key_fingerprint" character varying NOT NULL,
  "period_start" timestamptz NOT NULL,
  "requests" bigint NOT NULL DEFAULT 0,
  "backoffs" bigint NOT NULL DEFAULT 0,
  "updated_at" timestamptz NOT NULL,
  PRIMARY KEY ("provider", "key_fingerprint", "period_start")
);
-- Create "users" table
CREATE TABLE "users" (
  "id" uuid NOT NULL,
  "email" character varying NOT NULL,
  "name" character varying NOT NULL,
  "preferences" jsonb NOT NULL,
  "created_at" timestamptz NOT NULL,
  "updated_at" timestamptz NOT NULL,
  PRIMARY KEY ("id")
);
-- Create index "users_email_key" to table: "users"
CREATE UNIQUE INDEX "users_email_key" ON "users" ("email");
-- Create "portfolios" table
CREATE TABLE "portfolios" (
  "id" uuid NOT NULL,
  "created_at" timestamptz NOT NULL,
  "updated_at" timestamptz NOT NULL,
  "name" character varying NOT NULL,
  "description" character varying NULL,
  "data" jsonb NOT NULL,
  "user_id" uuid NOT NULL,
  PRIMARY KEY ("id"),
  CONSTRAINT "portfolios_users_portfolios" FOREIGN KEY ("user_id") REFERENCES "users" ("id") ON UPDATE NO ACTION ON DELETE NO ACTION
);
-- Create "accounts" table
CREATE TABLE "accounts" (
  "id" uuid NOT NULL,
  "created_at" timestamptz NOT NULL,
  "updated_at" timestamptz NOT NULL,
  "name" character varying NOT NULL,
  "description" character varying NULL,
  "type" character varying NOT NULL,
  "data" jsonb NOT NULL,
  "capabilities" jsonb NOT NULL DEFAULT '[]',
  "system_scopes" jsonb NOT NULL DEFAULT '[]',
  "user_id" uuid NOT NULL,
  "portfolio_id" uuid NULL,
  PRIMARY KEY ("id"),
  CONSTRAINT "accounts_portfolios_account" FOREIGN KEY ("portfolio_id") REFERENCES "portfolios" ("id") ON UPDATE NO ACTION ON DELETE SET NULL,
  CONSTRAINT "accounts_users_accounts" FOREIGN KEY ("user_id") REFERENCES "users" ("id") ON UPDATE NO ACTION ON DELETE NO ACTION
);
-- Create index "account_system_scopes" to table: "accounts"
CREATE INDEX "account_system_scopes" ON "accounts" USING GIN ("system_scopes");
-- Create "assets" table
CREATE TABLE "assets" (
  "id" uuid NOT NULL,
  "created_at" timestamptz NOT NULL,
  "updated_at" timestamptz NOT NULL,
  "symbol" character varying NOT NULL,
  "name" character varying NOT NULL,
  "type" character varying NOT NULL,
  "market" character varying NOT NULL DEFAULT 'crypto',
  "quote" character varying NULL,
  "tags" jsonb NOT NULL,
  "identity_verdict" character varying NOT NULL DEFAULT 'unknown',
  "identity_score" numeric NULL,
  "identity_signals" jsonb NULL,
  "verdict_source" character varying NULL,
  "verdict_set_at" timestamptz NULL,
  PRIMARY KEY ("id")
);
-- Create index "asset_symbol" to table: "assets"
CREATE INDEX "asset_symbol" ON "assets" ("symbol");
-- Create index "asset_symbol_market_type" to table: "assets"
CREATE UNIQUE INDEX "asset_symbol_market_type" ON "assets" ("symbol", "market", "type");
-- Create index "asset_tags" to table: "assets"
CREATE INDEX "asset_tags" ON "assets" USING GIN ("tags");
-- Create "asset_external_refs" table
CREATE TABLE "asset_external_refs" (
  "id" uuid NOT NULL,
  "created_at" timestamptz NOT NULL,
  "asset_id" uuid NOT NULL,
  "source" character varying NOT NULL,
  "ref" character varying NOT NULL,
  "origin" character varying NOT NULL DEFAULT 'auto',
  PRIMARY KEY ("id"),
  CONSTRAINT "asset_external_refs_assets" FOREIGN KEY ("asset_id") REFERENCES "assets" ("id") ON UPDATE NO ACTION ON DELETE CASCADE
);
-- Create index "asset_external_ref_asset_id" to table: "asset_external_refs"
CREATE INDEX "asset_external_ref_asset_id" ON "asset_external_refs" ("asset_id");
-- Create index "asset_external_ref_source_ref" to table: "asset_external_refs"
CREATE UNIQUE INDEX "asset_external_ref_source_ref" ON "asset_external_refs" ("source", "ref");
-- Create "asset_risk_flags" table
CREATE TABLE "asset_risk_flags" (
  "id" uuid NOT NULL,
  "created_at" timestamptz NOT NULL,
  "asset_id" uuid NOT NULL,
  "kind" character varying NOT NULL,
  "note" character varying NULL,
  "action_hint" character varying NULL,
  "review_at" timestamptz NULL,
  "set_by" character varying NULL,
  PRIMARY KEY ("id"),
  CONSTRAINT "asset_risk_flags_assets" FOREIGN KEY ("asset_id") REFERENCES "assets" ("id") ON UPDATE NO ACTION ON DELETE CASCADE
);
-- Create index "asset_risk_flag_asset_id" to table: "asset_risk_flags"
CREATE INDEX "asset_risk_flag_asset_id" ON "asset_risk_flags" ("asset_id");
-- Create "holdings" table
CREATE TABLE "holdings" (
  "id" uuid NOT NULL,
  "created_at" timestamptz NOT NULL,
  "updated_at" timestamptz NOT NULL,
  "amount" numeric NOT NULL,
  "decimals" bigint NOT NULL,
  "account_id" uuid NOT NULL,
  "asset_id" uuid NOT NULL,
  "portfolio_id" uuid NULL,
  "excluded" boolean NOT NULL DEFAULT false,
  "source" character varying NOT NULL DEFAULT 'sync',
  "chain" character varying NOT NULL DEFAULT '',
  "liquidity" character varying NOT NULL DEFAULT '',
  "import_id" uuid NULL,
  PRIMARY KEY ("id"),
  CONSTRAINT "holdings_accounts_holdings" FOREIGN KEY ("account_id") REFERENCES "accounts" ("id") ON UPDATE NO ACTION ON DELETE NO ACTION,
  CONSTRAINT "holdings_assets_holdings" FOREIGN KEY ("asset_id") REFERENCES "assets" ("id") ON UPDATE NO ACTION ON DELETE NO ACTION,
  CONSTRAINT "holdings_portfolios_holdings" FOREIGN KEY ("portfolio_id") REFERENCES "portfolios" ("id") ON UPDATE NO ACTION ON DELETE SET NULL
);
-- Create index "holding_account_asset_chain_liquidity" to table: "holdings"
CREATE INDEX "holding_account_asset_chain_liquidity" ON "holdings" ("account_id", "asset_id", "chain", "liquidity");
-- Create index "holding_import_id" to table: "holdings"
CREATE INDEX "holding_import_id" ON "holdings" ("import_id") WHERE (import_id IS NOT NULL);
-- Create "price_fetch_attempts" table
CREATE TABLE "price_fetch_attempts" (
  "asset_id" uuid NOT NULL,
  "source_id" character varying NOT NULL,
  "attempted_at" timestamptz NOT NULL,
  "succeeded_at" timestamptz NULL,
  "misses" integer NOT NULL DEFAULT 0,
  "next_attempt_at" timestamptz NOT NULL,
  PRIMARY KEY ("asset_id", "source_id"),
  CONSTRAINT "price_fetch_attempts_assets" FOREIGN KEY ("asset_id") REFERENCES "assets" ("id") ON UPDATE NO ACTION ON DELETE CASCADE
);
-- Create index "price_fetch_attempt_due" to table: "price_fetch_attempts"
CREATE INDEX "price_fetch_attempt_due" ON "price_fetch_attempts" ("source_id", "next_attempt_at");
-- Create "prices" table
CREATE TABLE "prices" (
  "id" uuid NOT NULL,
  "source_id" character varying NOT NULL,
  "interval" character varying NOT NULL,
  "decimals" bigint NOT NULL,
  "last" numeric NOT NULL,
  "open" numeric NULL,
  "high" numeric NULL,
  "low" numeric NULL,
  "close" numeric NULL,
  "volume" numeric NULL,
  "market_cap" numeric NULL,
  "provenance" character varying NULL,
  "timestamp" timestamptz NOT NULL,
  "asset_id" uuid NOT NULL,
  "base_asset_id" uuid NOT NULL,
  PRIMARY KEY ("id"),
  CONSTRAINT "prices_assets_asset" FOREIGN KEY ("asset_id") REFERENCES "assets" ("id") ON UPDATE NO ACTION ON DELETE NO ACTION,
  CONSTRAINT "prices_assets_base_asset" FOREIGN KEY ("base_asset_id") REFERENCES "assets" ("id") ON UPDATE NO ACTION ON DELETE RESTRICT,
  CONSTRAINT "price_pair_is_not_self" CHECK (asset_id <> base_asset_id)
);
-- Create index "price_asset_source_timestamp_base" to table: "prices"
CREATE UNIQUE INDEX "price_asset_source_timestamp_base" ON "prices" ("asset_id", "source_id", "timestamp", "base_asset_id");
-- Create "rules" table
CREATE TABLE "rules" (
  "id" uuid NOT NULL,
  "user_id" uuid NOT NULL,
  "portfolio_id" uuid NOT NULL,
  "name" character varying NOT NULL,
  "description" character varying NULL,
  "rule_type" character varying NOT NULL,
  "status" character varying NOT NULL,
  "configuration" jsonb NOT NULL,
  "schedule" jsonb NULL,
  "created_at" timestamptz NOT NULL,
  "updated_at" timestamptz NOT NULL,
  PRIMARY KEY ("id"),
  CONSTRAINT "rules_portfolios_rules" FOREIGN KEY ("portfolio_id") REFERENCES "portfolios" ("id") ON UPDATE NO ACTION ON DELETE NO ACTION,
  CONSTRAINT "rules_users_rules" FOREIGN KEY ("user_id") REFERENCES "users" ("id") ON UPDATE NO ACTION ON DELETE NO ACTION
);
-- Create index "rules_portfolio_id" to table: "rules"
CREATE INDEX "rules_portfolio_id" ON "rules" ("portfolio_id");
-- Create index "rules_status" to table: "rules"
CREATE INDEX "rules_status" ON "rules" ("status");
-- Create index "rules_user_id" to table: "rules"
CREATE INDEX "rules_user_id" ON "rules" ("user_id");
-- Create "rule_executions" table
CREATE TABLE "rule_executions" (
  "id" uuid NOT NULL,
  "rule_id" uuid NOT NULL,
  "portfolio_id" uuid NULL,
  "user_id" uuid NULL,
  "status" character varying NOT NULL,
  "error_message" character varying NULL,
  "created_transaction_ids" jsonb NOT NULL,
  "affected_holding_ids" jsonb NOT NULL,
  "transactions_created" bigint NOT NULL DEFAULT 0,
  "execution_summary" jsonb NULL,
  "started_at" timestamptz NOT NULL,
  "completed_at" timestamptz NULL,
  PRIMARY KEY ("id"),
  CONSTRAINT "rule_executions_portfolios" FOREIGN KEY ("portfolio_id") REFERENCES "portfolios" ("id") ON UPDATE NO ACTION ON DELETE SET NULL,
  CONSTRAINT "rule_executions_rules" FOREIGN KEY ("rule_id") REFERENCES "rules" ("id") ON UPDATE NO ACTION ON DELETE NO ACTION,
  CONSTRAINT "rule_executions_users" FOREIGN KEY ("user_id") REFERENCES "users" ("id") ON UPDATE NO ACTION ON DELETE SET NULL
);
-- Create index "rule_executions_rule_id" to table: "rule_executions"
CREATE INDEX "rule_executions_rule_id" ON "rule_executions" ("rule_id");
-- Create index "rule_executions_started_at" to table: "rule_executions"
CREATE INDEX "rule_executions_started_at" ON "rule_executions" ("started_at");
-- Create index "rule_executions_status" to table: "rule_executions"
CREATE INDEX "rule_executions_status" ON "rule_executions" ("status");
-- Create index "rule_executions_user_id" to table: "rule_executions"
CREATE INDEX "rule_executions_user_id" ON "rule_executions" ("user_id");
-- Create "transactions" table
CREATE TABLE "transactions" (
  "id" uuid NOT NULL,
  "type" character varying NOT NULL,
  "status" character varying NOT NULL,
  "data" jsonb NOT NULL,
  "created_at" timestamptz NOT NULL,
  "updated_at" timestamptz NOT NULL,
  "account_id" uuid NOT NULL,
  "asset_transactions" uuid NULL,
  "source" character varying NOT NULL DEFAULT 'sync',
  "import_id" uuid NULL,
  PRIMARY KEY ("id"),
  CONSTRAINT "transactions_accounts_transactions" FOREIGN KEY ("account_id") REFERENCES "accounts" ("id") ON UPDATE NO ACTION ON DELETE NO ACTION,
  CONSTRAINT "transactions_assets_transactions" FOREIGN KEY ("asset_transactions") REFERENCES "assets" ("id") ON UPDATE NO ACTION ON DELETE SET NULL
);
-- Create index "transaction_import_id" to table: "transactions"
CREATE INDEX "transaction_import_id" ON "transactions" ("import_id") WHERE (import_id IS NOT NULL);
-- Create "user_settings" table
CREATE TABLE "user_settings" (
  "user_id" uuid NOT NULL,
  "key" character varying NOT NULL,
  "value" jsonb NOT NULL,
  "updated_at" timestamptz NOT NULL,
  PRIMARY KEY ("user_id", "key"),
  CONSTRAINT "user_settings_users" FOREIGN KEY ("user_id") REFERENCES "users" ("id") ON UPDATE NO ACTION ON DELETE CASCADE
);
