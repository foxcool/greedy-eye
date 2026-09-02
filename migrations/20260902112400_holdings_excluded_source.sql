-- Modify "holdings" table
ALTER TABLE "holdings" ADD COLUMN "excluded_source" character varying NOT NULL DEFAULT '';
