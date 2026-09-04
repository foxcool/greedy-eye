-- Create "account_sync_attempts" table
CREATE TABLE "public"."account_sync_attempts" (
  "account_id" uuid NOT NULL,
  "attempted_at" timestamptz NOT NULL,
  "succeeded_at" timestamptz NULL,
  "misses" integer NOT NULL DEFAULT 0,
  "next_attempt_at" timestamptz NOT NULL,
  PRIMARY KEY ("account_id"),
  CONSTRAINT "account_sync_attempts_accounts" FOREIGN KEY ("account_id") REFERENCES "public"."accounts" ("id") ON UPDATE NO ACTION ON DELETE CASCADE
);
-- Create index "account_sync_attempt_due" to table: "account_sync_attempts"
CREATE INDEX "account_sync_attempt_due" ON "public"."account_sync_attempts" ("next_attempt_at");
