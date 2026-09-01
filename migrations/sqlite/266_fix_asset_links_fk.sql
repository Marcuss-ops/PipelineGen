-- database: primary
-- asset_links historically referenced asset_index(id), but asset_index owns
-- the canonical key as asset_id. Rebuild the empty/compatible table so the
-- foreign-key contract is valid and foreign_key_check can be authoritative.
CREATE TABLE asset_links_v266 (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    asset_id TEXT NOT NULL,
    link_type TEXT NOT NULL,
    url TEXT NOT NULL,
    label TEXT,
    FOREIGN KEY (asset_id) REFERENCES asset_index(asset_id) ON DELETE CASCADE
);
INSERT INTO asset_links_v266 (id, asset_id, link_type, url, label)
SELECT id, asset_id, link_type, url, label FROM asset_links;
DROP TABLE asset_links;
ALTER TABLE asset_links_v266 RENAME TO asset_links;
