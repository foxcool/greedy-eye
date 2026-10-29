-- Create "price_unasked_deferrals" table
CREATE TABLE "public"."price_unasked_deferrals" (
  "asset_id" uuid NOT NULL,
  "source_id" character varying NOT NULL,
  "considered_at" timestamptz NOT NULL,
  "reconsider_at" timestamptz NOT NULL,
  PRIMARY KEY ("asset_id", "source_id"),
  CONSTRAINT "price_unasked_deferrals_assets" FOREIGN KEY ("asset_id") REFERENCES "public"."assets" ("id") ON UPDATE NO ACTION ON DELETE CASCADE
);
