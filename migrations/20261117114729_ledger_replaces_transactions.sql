-- Create "ledger_events" table
CREATE TABLE "public"."ledger_events" (
  "id" uuid NOT NULL,
  "account_id" uuid NOT NULL,
  "kind" text NOT NULL,
  "executed_at" timestamptz NOT NULL,
  "external_ref" text NULL,
  "source" text NOT NULL,
  "import_id" uuid NULL,
  "raw" jsonb NOT NULL,
  "created_at" timestamptz NOT NULL,
  "updated_at" timestamptz NOT NULL,
  PRIMARY KEY ("id"),
  CONSTRAINT "ledger_events_accounts" FOREIGN KEY ("account_id") REFERENCES "public"."accounts" ("id") ON UPDATE NO ACTION ON DELETE NO ACTION,
  CONSTRAINT "ledger_events_kind" CHECK (kind = ANY (ARRAY['trade'::text, 'transfer_in'::text, 'transfer_out'::text, 'deposit'::text, 'withdrawal'::text, 'income'::text, 'fee'::text, 'opening'::text]))
);
-- Create index "ledger_events_account_executed_at" to table: "ledger_events"
CREATE INDEX "ledger_events_account_executed_at" ON "public"."ledger_events" ("account_id", "executed_at", "id");
-- Create index "ledger_events_external_ref" to table: "ledger_events"
CREATE UNIQUE INDEX "ledger_events_external_ref" ON "public"."ledger_events" ("account_id", "source", "external_ref") WHERE (external_ref IS NOT NULL);
-- Create "ledger_legs" table
CREATE TABLE "public"."ledger_legs" (
  "id" uuid NOT NULL,
  "event_id" uuid NOT NULL,
  "asset_id" uuid NOT NULL,
  "direction" text NOT NULL,
  "role" text NOT NULL,
  "amount" numeric NOT NULL,
  "decimals" integer NOT NULL,
  "chain" text NULL,
  PRIMARY KEY ("id"),
  CONSTRAINT "ledger_legs_assets" FOREIGN KEY ("asset_id") REFERENCES "public"."assets" ("id") ON UPDATE NO ACTION ON DELETE NO ACTION,
  CONSTRAINT "ledger_legs_events" FOREIGN KEY ("event_id") REFERENCES "public"."ledger_events" ("id") ON UPDATE NO ACTION ON DELETE CASCADE,
  CONSTRAINT "ledger_legs_amount_integer" CHECK (amount = trunc(amount)),
  CONSTRAINT "ledger_legs_amount_positive" CHECK (amount > (0)::numeric),
  CONSTRAINT "ledger_legs_decimals_not_negative" CHECK (decimals >= 0),
  CONSTRAINT "ledger_legs_direction" CHECK (direction = ANY (ARRAY['in'::text, 'out'::text])),
  CONSTRAINT "ledger_legs_role" CHECK (role = ANY (ARRAY['principal'::text, 'counter'::text, 'fee'::text, 'income'::text]))
);
-- Create index "ledger_legs_asset_id" to table: "ledger_legs"
CREATE INDEX "ledger_legs_asset_id" ON "public"."ledger_legs" ("asset_id");
-- Create index "ledger_legs_event_id" to table: "ledger_legs"
CREATE INDEX "ledger_legs_event_id" ON "public"."ledger_legs" ("event_id");
-- Drop "transactions" table
DROP TABLE "public"."transactions";
