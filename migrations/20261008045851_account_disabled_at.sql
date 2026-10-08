-- Modify "accounts" table
ALTER TABLE "public"."accounts" ADD COLUMN "disabled_at" timestamptz NULL;
