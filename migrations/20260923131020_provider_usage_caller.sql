-- Modify "provider_usage" table
ALTER TABLE "public"."provider_usage" DROP CONSTRAINT "provider_usage_pkey", ADD COLUMN "caller" character varying NOT NULL DEFAULT '', ADD PRIMARY KEY ("provider", "key_fingerprint", "period_start", "caller");
