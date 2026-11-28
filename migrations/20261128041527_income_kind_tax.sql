-- Modify "ledger_events" table
ALTER TABLE "public"."ledger_events" DROP CONSTRAINT "ledger_events_income_kind", ADD CONSTRAINT "ledger_events_income_kind" CHECK ((income_kind IS NULL) OR ((kind = 'income'::text) AND (income_kind = ANY (ARRAY['coupon'::text, 'dividend'::text, 'interest'::text, 'staking_reward'::text, 'other'::text, 'tax'::text]))));
