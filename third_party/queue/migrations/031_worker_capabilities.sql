-- Worker capability inventory is the authoritative renderer contract for
-- producer preflight. An empty JSON array means the live worker advertises no
-- optional capabilities; it must never be inferred from build flags.
ALTER TABLE rendering_workers
    ADD COLUMN capabilities JSONB NOT NULL DEFAULT '[]'::jsonb;
