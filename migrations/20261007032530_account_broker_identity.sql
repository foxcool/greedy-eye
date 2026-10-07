-- Create index "account_broker_identity" to table: "accounts"
CREATE UNIQUE INDEX "account_broker_identity" ON "public"."accounts" ("user_id", ((data ->> 'provider'::text)), ((data ->> 'broker_account_id'::text))) WHERE (((type)::text = 'broker'::text) AND (data ? 'broker_account_id'::text));
