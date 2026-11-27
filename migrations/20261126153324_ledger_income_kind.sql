-- Modify "ledger_events" table
ALTER TABLE "public"."ledger_events" ADD CONSTRAINT "ledger_events_income_kind" CHECK ((income_kind IS NULL) OR ((kind = 'income'::text) AND (income_kind = ANY (ARRAY['coupon'::text, 'dividend'::text, 'interest'::text, 'staking_reward'::text, 'other'::text])))), ADD COLUMN "income_kind" text NULL;
-- Create index "ledger_events_income" to table: "ledger_events"
CREATE INDEX "ledger_events_income" ON "public"."ledger_events" ("account_id", "executed_at") WHERE (kind = 'income'::text);
