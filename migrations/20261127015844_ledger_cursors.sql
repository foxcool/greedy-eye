-- Create "ledger_cursors" table
CREATE TABLE "public"."ledger_cursors" (
  "account_id" uuid NOT NULL,
  "chain" text NOT NULL,
  "source" text NOT NULL,
  "next_block" bigint NOT NULL,
  "addresses" text NOT NULL,
  "incomplete" text NULL,
  "last_error" text NULL,
  "checked_at" timestamptz NOT NULL,
  "advanced_at" timestamptz NOT NULL,
  PRIMARY KEY ("account_id", "chain"),
  CONSTRAINT "ledger_cursors_accounts" FOREIGN KEY ("account_id") REFERENCES "public"."accounts" ("id") ON UPDATE NO ACTION ON DELETE CASCADE,
  CONSTRAINT "ledger_cursors_next_block_not_negative" CHECK (next_block >= 0)
);
