-- Modify "holdings" table
ALTER TABLE "public"."holdings" ADD COLUMN "synced_at" timestamptz NULL;
