-- PR: script_sections now carries per-section voiceover links so the
-- GET /api/scripts/:id response exposes voiceover_per_scene in a
-- first-class field instead of burying it in the specscene JSON blob.
ALTER TABLE script_sections ADD COLUMN voiceover_link TEXT NOT NULL DEFAULT '';
