-- Modify "ledger_events" table
ALTER TABLE "public"."ledger_events" ADD CONSTRAINT "ledger_events_payer" CHECK ((payer_asset_id IS NULL) OR ((kind = 'income'::text) AND (income_kind IS DISTINCT FROM 'tax'::text))), ADD COLUMN "payer_asset_id" uuid NULL, ADD CONSTRAINT "ledger_events_payer_assets" FOREIGN KEY ("payer_asset_id") REFERENCES "public"."assets" ("id") ON UPDATE NO ACTION ON DELETE NO ACTION;
-- Create index "ledger_events_payer_asset_id" to table: "ledger_events"
CREATE INDEX "ledger_events_payer_asset_id" ON "public"."ledger_events" ("payer_asset_id") WHERE (payer_asset_id IS NOT NULL);
