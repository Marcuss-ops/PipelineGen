CREATE TABLE IF NOT EXISTS entity_image_catalog_materialization_attempts (
    attempt_id      INTEGER PRIMARY KEY AUTOINCREMENT,
    candidate_id    INTEGER NOT NULL,
    asset_id        TEXT NOT NULL DEFAULT '',
    content_sha256  TEXT NOT NULL DEFAULT '',
    status          TEXT NOT NULL DEFAULT '',
    last_error      TEXT NOT NULL DEFAULT '',
    attempted_at    TEXT NOT NULL,
    FOREIGN KEY (candidate_id)
        REFERENCES entity_image_catalog_candidates(candidate_id)
        ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_entity_image_catalog_materialization_attempts_candidate
    ON entity_image_catalog_materialization_attempts(candidate_id, attempt_id);
