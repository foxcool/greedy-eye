-- Repair rows the old re-include path left with an author on an included
-- holding; the check below refuses them, so they must go first.
UPDATE "public"."holdings" SET "excluded_source" = '' WHERE NOT "excluded" AND "excluded_source" <> '';
-- Modify "holdings" table
ALTER TABLE "public"."holdings" ADD CONSTRAINT "holding_exclusion_author_requires_exclusion" CHECK (excluded OR ((excluded_source)::text = ''::text));
