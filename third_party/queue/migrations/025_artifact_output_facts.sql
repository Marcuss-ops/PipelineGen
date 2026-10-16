-- Persist the COMPLETE structural certification of a rendered artifact.
--
-- render_artifacts historically persisted only a subset of the ffprobe facts
-- (container, pixel_format, audio_streams, codec, codec_profile, ...). The
-- clip.render output contract validates more dimensions than that — video
-- timebase, SAR, colour range/space/transfer/primaries, field order, GOP
-- interval, stream layout, start PTS and the full audio block — and a consumer
-- that cannot observe a dimension itself silently SKIPPED its check.
--
-- The certified probe is the owner of those facts, so it is persisted as one
-- JSON object (queue/client.OutputFacts). JSONB keeps the fact set extensible
-- without a database column per fact, and NULL means "a legacy worker did not
-- certify the full set" — never "the facts are empty".
ALTER TABLE render_artifacts
    ADD COLUMN IF NOT EXISTS output_facts JSONB;
