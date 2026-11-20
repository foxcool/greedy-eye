-- Create "sources" table
CREATE TABLE "public"."sources" (
  "id" uuid NOT NULL,
  "kind" text NOT NULL,
  "url" text NOT NULL,
  "name" text NOT NULL,
  "enabled" boolean NOT NULL DEFAULT true,
  "cron" text NOT NULL,
  "weight" integer NOT NULL DEFAULT 1,
  "last_fetched_at" timestamptz NULL,
  "last_success_at" timestamptz NULL,
  "last_error" text NULL,
  "last_error_at" timestamptz NULL,
  "created_at" timestamptz NOT NULL,
  "updated_at" timestamptz NOT NULL,
  PRIMARY KEY ("id"),
  CONSTRAINT "sources_kind" CHECK (kind = 'rss'::text),
  CONSTRAINT "sources_weight_not_negative" CHECK (weight >= 0)
);
-- Create index "sources_url_key" to table: "sources"
CREATE UNIQUE INDEX "sources_url_key" ON "public"."sources" ("url");
-- Create "documents" table
CREATE TABLE "public"."documents" (
  "id" uuid NOT NULL,
  "source_id" uuid NOT NULL,
  "url" text NOT NULL,
  "title" text NOT NULL,
  "snippet" text NOT NULL,
  "published_at" timestamptz NOT NULL,
  "fetched_at" timestamptz NOT NULL,
  "url_hash" text NOT NULL,
  PRIMARY KEY ("id"),
  CONSTRAINT "documents_sources" FOREIGN KEY ("source_id") REFERENCES "public"."sources" ("id") ON UPDATE NO ACTION ON DELETE CASCADE,
  CONSTRAINT "documents_snippet_length" CHECK (char_length(snippet) <= 300),
  CONSTRAINT "documents_title_length" CHECK (char_length(title) <= 300),
  CONSTRAINT "documents_url_length" CHECK (char_length(url) <= 2048)
);
-- Create index "documents_published_at" to table: "documents"
CREATE INDEX "documents_published_at" ON "public"."documents" ("published_at", "id");
-- Create index "documents_source_published_at" to table: "documents"
CREATE INDEX "documents_source_published_at" ON "public"."documents" ("source_id", "published_at", "id");
-- Create index "documents_url_hash_key" to table: "documents"
CREATE UNIQUE INDEX "documents_url_hash_key" ON "public"."documents" ("url_hash");
