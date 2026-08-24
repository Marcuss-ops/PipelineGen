-- migrations/sqlite/037_seed_voiceover_categories.sql
--
-- NOTE: this file was renumbered from 035 to 037 to resolve a duplicate
-- version collision (two .sql files previously claimed v035). The actual
-- schema work for v035 stays in 035_artifact_registry_unify.sql.
--
-- Idempotent seed of the 9 voiceover category folders under the shared
-- Voiceover root (1wFhLmyyIH5rKSbtQuCuua9a2LKQymA8A) into asset_tree_nodes.
-- INSERT OR IGNORE so re-running on a populated DB is a no-op. Names are
-- stored verbatim from the operator (with trailing spaces where Drive has
-- them — keep them so case-sensitive lookups match).
--
-- After this runs, GET /api/media/voiceover/groups will return the mapping
-- and any endpoint that accepts voiceover_group can resolve it via
-- internal/platform/sqlite/assettree_repository (FindByName)
-- without falling back to Drive-side deep search.

INSERT OR IGNORE INTO asset_tree_nodes (
    id, source, asset_id, name, type, parent_id, root_id, path, depth,
    is_folder, drive_file_id, drive_link, metadata, created_at, updated_at
) VALUES
    ('drive-folder-1oOlaSOwq1P7_yLfanvBqxwMvEoV1n4Wo', 'drive', '1oOlaSOwq1P7_yLfanvBqxwMvEoV1n4Wo',
     'Boxe',         'folder', '1wFhLmyyIH5rKSbtQuCuua9a2LKQymA8A', '1wFhLmyyIH5rKSbtQuCuua9a2LKQymA8A',
     '/Voiceover/Boxe',         1, 1,
     '1oOlaSOwq1P7_yLfanvBqxwMvEoV1n4Wo',
     'https://drive.google.com/drive/folders/1oOlaSOwq1P7_yLfanvBqxwMvEoV1n4Wo',
     '{"kind":"voiceover_category"}', datetime('now'), datetime('now')),

    ('drive-folder-1bNb14kz0m4Vxd_F3af8lcIL-bgvZFJ6P', 'drive', '1bNb14kz0m4Vxd_F3af8lcIL-bgvZFJ6P',
     'Comedy',       'folder', '1wFhLmyyIH5rKSbtQuCuua9a2LKQymA8A', '1wFhLmyyIH5rKSbtQuCuua9a2LKQymA8A',
     '/Voiceover/Comedy',       1, 1,
     '1bNb14kz0m4Vxd_F3af8lcIL-bgvZFJ6P',
     'https://drive.google.com/drive/folders/1bNb14kz0m4Vxd_F3af8lcIL-bgvZFJ6P',
     '{"kind":"voiceover_category"}', datetime('now'), datetime('now')),

    ('drive-folder-1yhqumS6yG91ZDFBzxeJWXgsUP7mVPXfL', 'drive', '1yhqumS6yG91ZDFBzxeJWXgsUP7mVPXfL',
     'Crime',        'folder', '1wFhLmyyIH5rKSbtQuCuua9a2LKQymA8A', '1wFhLmyyIH5rKSbtQuCuua9a2LKQymA8A',
     '/Voiceover/Crime',        1, 1,
     '1yhqumS6yG91ZDFBzxeJWXgsUP7mVPXfL',
     'https://drive.google.com/drive/folders/1yhqumS6yG91ZDFBzxeJWXgsUP7mVPXfL',
     '{"kind":"voiceover_category"}', datetime('now'), datetime('now')),

    ('drive-folder-1655kxyQMiJzN5Ugwh8uzNUdEgVJr3O9O', 'drive', '1655kxyQMiJzN5Ugwh8uzNUdEgVJr3O9O',
     'Discovery',    'folder', '1wFhLmyyIH5rKSbtQuCuua9a2LKQymA8A', '1wFhLmyyIH5rKSbtQuCuua9a2LKQymA8A',
     '/Voiceover/Discovery',    1, 1,
     '1655kxyQMiJzN5Ugwh8uzNUdEgVJr3O9O',
     'https://drive.google.com/drive/folders/1655kxyQMiJzN5Ugwh8uzNUdEgVJr3O9O',
     '{"kind":"voiceover_category"}', datetime('now'), datetime('now')),

    ('drive-folder-1BkxSjbV4Dysv_XffuHmqnfDxg5d0Xs9N', 'drive', '1BkxSjbV4Dysv_XffuHmqnfDxg5d0Xs9N',
     'Explainatory ', 'folder', '1wFhLmyyIH5rKSbtQuCuua9a2LKQymA8A', '1wFhLmyyIH5rKSbtQuCuua9a2LKQymA8A',
     '/Voiceover/Explainatory ', 1, 1,
     '1BkxSjbV4Dysv_XffuHmqnfDxg5d0Xs9N',
     'https://drive.google.com/drive/folders/1BkxSjbV4Dysv_XffuHmqnfDxg5d0Xs9N',
     '{"kind":"voiceover_category"}', datetime('now'), datetime('now')),

    ('drive-folder-1bR5XyiB04bJxaUyQGpWNqN9BVgXAkc1C', 'drive', '1bR5XyiB04bJxaUyQGpWNqN9BVgXAkc1C',
     'HIpHop',       'folder', '1wFhLmyyIH5rKSbtQuCuua9a2LKQymA8A', '1wFhLmyyIH5rKSbtQuCuua9a2LKQymA8A',
     '/Voiceover/HIpHop',       1, 1,
     '1bR5XyiB04bJxaUyQGpWNqN9BVgXAkc1C',
     'https://drive.google.com/drive/folders/1bR5XyiB04bJxaUyQGpWNqN9BVgXAkc1C',
     '{"kind":"voiceover_category"}', datetime('now'), datetime('now')),

    ('drive-folder-120d5xpzKN4rE5obIC16AtG_66NXJrlF0', 'drive', '120d5xpzKN4rE5obIC16AtG_66NXJrlF0',
     'Music',        'folder', '1wFhLmyyIH5rKSbtQuCuua9a2LKQymA8A', '1wFhLmyyIH5rKSbtQuCuua9a2LKQymA8A',
     '/Voiceover/Music',        1, 1,
     '120d5xpzKN4rE5obIC16AtG_66NXJrlF0',
     'https://drive.google.com/drive/folders/120d5xpzKN4rE5obIC16AtG_66NXJrlF0',
     '{"kind":"voiceover_category"}', datetime('now'), datetime('now')),

    ('drive-folder-1lSp-s8mNJOUOxIZbuZ0NjvzbXVMB1Y3I', 'drive', '1lSp-s8mNJOUOxIZbuZ0NjvzbXVMB1Y3I',
     'Wwe',          'folder', '1wFhLmyyIH5rKSbtQuCuua9a2LKQymA8A', '1wFhLmyyIH5rKSbtQuCuua9a2LKQymA8A',
     '/Voiceover/Wwe',          1, 1,
     '1lSp-s8mNJOUOxIZbuZ0NjvzbXVMB1Y3I',
     'https://drive.google.com/drive/folders/1lSp-s8mNJOUOxIZbuZ0NjvzbXVMB1Y3I',
     '{"kind":"voiceover_category"}', datetime('now'), datetime('now'));


