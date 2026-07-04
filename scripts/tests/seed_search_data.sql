-- Seed test data for Search Aggregata tests
-- YouTube clips (video)
INSERT INTO media_assets (id, source, name, media_type, lifecycle_state, search_text, tags, tags_norm, language, youtube_video_id, youtube_url, duration_ms, thumb_url, category, created_at, updated_at, index_state)
VALUES ('yt-mayweather-001', 'youtube', 'Floyd Mayweather - Best Knockouts Highlights', 'video', 'ACTIVE', 'Floyd Mayweather best knockouts boxing highlights GOAT undefeated champion', '["boxing","knockout","mayweather","highlights"]', '["boxing","knockout","mayweather","highlights"]', 'en', '9u4T_o3FxOU', 'https://www.youtube.com/watch?v=9u4T_o3FxOU', 480000, 'https://i.ytimg.com/vi/9u4T_o3FxOU/default.jpg', 'sports', datetime('now'), datetime('now'), 'INDEXED');

INSERT INTO media_assets (id, source, name, media_type, lifecycle_state, search_text, tags, tags_norm, language, youtube_video_id, youtube_url, duration_ms, thumb_url, category, created_at, updated_at, index_state)
VALUES ('yt-boxing-training-002', 'youtube', 'Boxing Training: Footwork Drills for Beginners', 'video', 'ACTIVE', 'boxing training footwork drills beginners techniques stance movement', '["boxing","training","footwork","drills","beginner"]', '["boxing","training","footwork","drills","beginner"]', 'en', 'dQw4w9WgXcQ', 'https://www.youtube.com/watch?v=dQw4w9WgXcQ', 720000, 'https://i.ytimg.com/vi/dQw4w9WgXcQ/default.jpg', 'sports', datetime('now'), datetime('now'), 'INDEXED');

INSERT INTO media_assets (id, source, name, media_type, lifecycle_state, search_text, tags, tags_norm, language, youtube_video_id, youtube_url, duration_ms, category, created_at, updated_at, index_state)
VALUES ('yt-deleted-003', 'youtube', 'Old Boxing Match - Deleted', 'video', 'DELETED', 'old boxing match deleted archive', '["boxing","deleted","archive"]', '["boxing","deleted","archive"]', 'en', 'xxxxxxxxxxx', 'https://www.youtube.com/watch?v=xxxxxxxxxxx', 360000, 'sports', datetime('now'), datetime('now'), 'INDEXED');

-- Voiceover audio clips (Italian + English + Spanish)
INSERT INTO media_assets (id, source, name, media_type, lifecycle_state, search_text, tags, tags_norm, language, duration_ms, category, created_at, updated_at, index_state, file_hash)
VALUES ('vo-italian-intro-001', 'voiceover', 'Introduzione al documentario - IT', 'audio', 'ACTIVE', 'documentario italiano introduzione cinema storia arte', '["documentario","italiano","introduzione","cinema"]', '["documentario","italiano","introduzione","cinema"]', 'it-IT', 45000, 'documentary', datetime('now'), datetime('now'), 'INDEXED', 'abc123hashvo001');

INSERT INTO media_assets (id, source, name, media_type, lifecycle_state, search_text, tags, tags_norm, language, duration_ms, category, created_at, updated_at, index_state, file_hash)
VALUES ('vo-english-narration-002', 'voiceover', 'Boxing Documentary Narration - EN', 'audio', 'ACTIVE', 'boxing documentary narration english heavyweight champion', '["boxing","documentary","narration","english"]', '["boxing","documentary","narration","english"]', 'en', 60000, 'documentary', datetime('now'), datetime('now'), 'INDEXED', 'def456hashvo002');

INSERT INTO media_assets (id, source, name, media_type, lifecycle_state, search_text, tags, tags_norm, language, duration_ms, category, created_at, updated_at, index_state)
VALUES ('vo-sp-narration-003', 'voiceover', 'Narracion de boxeo - ES', 'audio', 'ACTIVE', 'narracion boxeo espanol combate knockout', '["boxeo","narracion","espanol","combate"]', '["boxeo","narracion","espanol","combate"]', 'es', 35000, 'sports', datetime('now'), datetime('now'), 'INDEXED');

-- Artlist video clips
INSERT INTO media_assets (id, source, name, media_type, lifecycle_state, search_text, tags, tags_norm, duration_ms, thumb_url, category, created_at, updated_at, index_state)
VALUES ('al-city-night-001', 'artlist', 'City Night Cinematic Aerial View', 'video', 'ACTIVE', 'city night cinematic aerial drone urban lights skyline', '["city","night","cinematic","aerial","drone"]', '["city","night","cinematic","aerial","drone"]', 15000, 'https://cdn.artlist.io/thumb/al-city-night-001.jpg', 'cinematic', datetime('now'), datetime('now'), 'INDEXED');

INSERT INTO media_assets (id, source, name, media_type, lifecycle_state, search_text, tags, tags_norm, duration_ms, thumb_url, category, created_at, updated_at, index_state)
VALUES ('al-boxing-ring-002', 'artlist', 'Boxing Ring Empty Arena Slow Motion', 'video', 'ACTIVE', 'boxing ring empty arena slow motion dramatic spotlight', '["boxing","ring","arena","slow-motion","dramatic"]', '["boxing","ring","arena","slow-motion","dramatic"]', 12000, 'https://cdn.artlist.io/thumb/al-boxing-ring-002.jpg', 'sports', datetime('now'), datetime('now'), 'INDEXED');

INSERT INTO media_assets (id, source, name, media_type, lifecycle_state, search_text, tags, tags_norm, duration_ms, thumb_url, category, created_at, updated_at, index_state)
VALUES ('al-coastal-sunset-003', 'artlist', 'Coastal Sunset Timelapse 4K', 'video', 'ACTIVE', 'coastal sunset timelapse 4k ocean waves golden hour', '["coastal","sunset","timelapse","ocean","4k"]', '["coastal","sunset","timelapse","ocean","4k"]', 20000, 'https://cdn.artlist.io/thumb/al-coastal-sunset-003.jpg', 'nature', datetime('now'), datetime('now'), 'INDEXED');

-- Verify
SELECT '=== Inserted assets by source ===';
SELECT source, COUNT(*) as count FROM media_assets GROUP BY source;
SELECT '=== Assets by lifecycle_state ===';
SELECT lifecycle_state, COUNT(*) as count FROM media_assets GROUP BY lifecycle_state;
SELECT '=== Full asset list ===';
SELECT id, source, name, media_type, lifecycle_state, language FROM media_assets ORDER BY source, name;
