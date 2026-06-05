-- Create "comments" table
CREATE TABLE "comments" (
  "id" bigserial NOT NULL,
  "task_id" bigint NOT NULL,
  "body" character varying NOT NULL,
  "created_at" timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
  "updated_at" timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY ("id")
);
