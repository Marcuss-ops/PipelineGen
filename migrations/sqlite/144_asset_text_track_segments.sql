-- 144_asset_text_track_segments.sql
--
-- asset_text_track_segments table (PR-PY-CLIPS-CORRETTE-TRADOTTE
-- Fase 2.a inline pre-empt, July 2026).
--
-- Each row is one timed cue associated with a track in
-- asset_text_tracks. Multiple cues per track are stored as
-- multiple rows (sequence_no, start_ms, end_ms, text) so the
-- rendering pipeline can read them in order without re-parsing
-- the VTT.
--
-- FK ON DELETE CASCADE: when the parent asset_text_tracks row is
-- deleted, all segments are deleted. The asset row in
-- media_assets is NOT deleted — that's the canonical lifecycle
-- state flip (asset_lifecycle.ACTIVE→DELETED), a separate
-- concern owned by AssetMutationDispatcher.
--
-- UNIQUE(track_id, sequence_no): prevents two segments at the
-- same ordinal within a track. The cue text is NOT part of the
-- uniqueness key because two adjacent cues MAY legitimately
-- share the same text (sustained silence or repeated music
-- cues).
--
-- Sequence_no assignment is the application layer's
-- responsibility; the resolver bundle assigns sequence_no at
-- persist time from the in-memory array index of
-- ResolvedTextBundle.Cues (asset.TimedCue.SequenceNo is empty
-- by convention; the writer auto-fills it on insert).
--
-- godlike/06 SSOT: this is the SOLE canonical schema for the
-- per-cue timings consumed by the video re-rendering + future
-- caption styling path. Mirror callers MUST call the writer,
-- not re-implement the SQL locally.

CREATE TABLE IF NOT EXISTS asset_text_track_segments (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    track_id    INTEGER NOT NULL,
    sequence_no INTEGER NOT NULL,
    start_ms    INTEGER NOT NULL,
    end_ms      INTEGER NOT NULL,
    text        TEXT NOT NULL,

    FOREIGN KEY (track_id)
        REFERENCES asset_text_tracks(id)
        ON DELETE CASCADE,
    UNIQUE(track_id, sequence_no)
);

CREATE INDEX IF NOT EXISTS idx_asset_text_track_segments_track
    ON asset_text_track_segments(track_id, sequence_no);
