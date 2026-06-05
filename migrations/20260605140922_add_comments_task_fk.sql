-- Add a foreign key from comments.task_id to tasks.id.
-- Manual because the Bun provider does not emit FK constraints from the model.
ALTER TABLE "comments"
  ADD CONSTRAINT "comments_task_id_fkey"
  FOREIGN KEY ("task_id") REFERENCES "tasks" ("id") ON DELETE CASCADE;
