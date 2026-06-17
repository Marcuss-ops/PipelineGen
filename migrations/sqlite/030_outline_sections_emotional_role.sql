-- 030_outline_sections_emotional_role.sql
-- Adds emotional_role to the outline sections table so that the
-- ScriptPlan persisted in DB carries not just purpose + key_points
-- but also the target emotion (curiosity, tension, relief, etc.)
-- that the chapter is supposed to evoke.
--
-- The column is added with a default of '' so the migration is
-- safe to run on existing rows.

ALTER TABLE script_outline_sections
    ADD COLUMN emotional_role TEXT NOT NULL DEFAULT '';
