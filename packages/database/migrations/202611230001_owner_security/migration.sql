ALTER TABLE "Owner"
  ADD COLUMN "securityRevision" bigint NOT NULL DEFAULT 1,
  ADD COLUMN "passkeyHandle" uuid NOT NULL DEFAULT gen_random_uuid(),
  ADD COLUMN "totpSecret" text NOT NULL DEFAULT '',
  ADD COLUMN "totpStep" bigint NOT NULL DEFAULT -1,
  ADD COLUMN "recoveryHashes" text[] NOT NULL DEFAULT '{}';

CREATE TABLE "OwnerPasskey" (
  "id" bytea PRIMARY KEY,
  "ownerId" integer NOT NULL REFERENCES "Owner"("id") ON DELETE CASCADE,
  "name" text NOT NULL,
  "credential" jsonb NOT NULL
);
