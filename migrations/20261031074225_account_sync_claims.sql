-- Create "account_sync_claims" table
CREATE TABLE "public"."account_sync_claims" (
  "account_id" uuid NOT NULL,
  "token" uuid NOT NULL,
  "claimed_until" timestamptz NOT NULL,
  PRIMARY KEY ("account_id"),
  CONSTRAINT "account_sync_claims_accounts" FOREIGN KEY ("account_id") REFERENCES "public"."accounts" ("id") ON UPDATE NO ACTION ON DELETE CASCADE
);
