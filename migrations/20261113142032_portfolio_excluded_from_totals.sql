-- Modify "portfolios" table
ALTER TABLE "public"."portfolios" ADD COLUMN "excluded_from_totals" boolean NOT NULL DEFAULT false;
