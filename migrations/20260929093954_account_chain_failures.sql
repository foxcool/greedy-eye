-- Create "account_chain_failures" table
CREATE TABLE "public"."account_chain_failures" (
  "account_id" uuid NOT NULL,
  "chain" text NOT NULL,
  "failing_since" timestamptz NOT NULL,
  "last_failed_at" timestamptz NOT NULL,
  "failures" integer NOT NULL DEFAULT 1,
  "last_error" text NOT NULL,
  PRIMARY KEY ("account_id", "chain"),
  CONSTRAINT "account_chain_failures_accounts" FOREIGN KEY ("account_id") REFERENCES "public"."accounts" ("id") ON UPDATE NO ACTION ON DELETE CASCADE
);
