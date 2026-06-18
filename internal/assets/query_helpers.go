package assets

// mediaAssetColumns defines the columns selected from media_assets table.
const mediaAssetColumns = `
	id,
	COALESCE(source, '') AS source,
	COALESCE(name, '') AS name,
	COALESCE(tags, '[]') AS tags,
	COALESCE(tags_norm, '') AS tags_norm,
	COALESCE(embedding_json, '[]') AS embedding_json,
	COALESCE(duration_ms, 0) AS duration_ms,
	COALESCE(url, '') AS url,
	COALESCE(media_type, '') AS media_type,
	COALESCE(status, '') AS status,
	COALESCE(local_path, '') AS local_path,
	COALESCE(relative_path, '') AS relative_path,
	COALESCE(drive_file_id, '') AS drive_file_id,
	COALESCE(folder_id, '') AS drive_folder_id,
	COALESCE(drive_link, '') AS drive_link,
	COALESCE(download_link, '') AS download_link,
	COALESCE(file_hash, '') AS file_hash,
	COALESCE(metadata_json, '{}') AS metadata_json,
	COALESCE(visual_embedding, '[]') AS visual_embedding,
	COALESCE(transcript_embedding, '[]') AS transcript_embedding,
	created_at,
	COALESCE(updated_at, '') AS updated_at,
	COALESCE(width, 0) AS width,
	COALESCE(height, 0) AS height,
	COALESCE(lifecycle_state, 'ready') AS lifecycle_state,
	COALESCE(deleted_at, '') AS deleted_at,
	COALESCE(folder_id, '') AS folder_id,
	COALESCE(parent_folder_id, '') AS parent_folder_id,
	COALESCE(folder_path, '') AS folder_path,
	COALESCE(category, '') AS category,
	COALESCE(filename, '') AS filename,
	COALESCE(error, '') AS error,
	COALESCE(thumb_url, '') AS thumb_url,
	COALESCE(phash, '') AS phash,
	COALESCE(search_text, '') AS search_text,
	COALESCE(scene_type, '') AS scene_type,
	COALESCE(quality_score, 0.0) AS quality_score,
	COALESCE(reuse_count, 0) AS reuse_count,
	COALESCE(last_used_at, '') AS last_used_at`

const clipFolderColumns = `id, source, COALESCE(source_url, '') AS source_url, COALESCE(video_id, '') AS video_id, COALESCE(folder_id, '') AS folder_id, COALESCE(folder_path, '') AS folder_path, COALESCE(local_folder_path, '') AS local_folder_path, COALESCE(group_name, '') AS group_name, COALESCE(manifest_txt_path, '') AS manifest_txt_path, COALESCE(manifest_json_path, '') AS manifest_json_path, clip_count, processed_count, failed_count, skipped_count, COALESCE(last_error, '') AS last_error, COALESCE(metadata, '{}') AS metadata, created_at, updated_at`

func SoftDeleteFilter() string {
	return "lifecycle_state != 'deleted'"
}

func buildMediaAssetQuery(source string) string {
	query := "SELECT " + mediaAssetColumns + " FROM media_assets WHERE " + SoftDeleteFilter()
	if source != "" && source != "all" && source != "unified" {
		query += " AND source = ?"
	}
	return query
}

func buildClipFolderQuery(source string) string {
	query := "SELECT " + clipFolderColumns + " FROM clip_folders"
	if source != "" && source != "all" && source != "unified" {
		query += " WHERE source = ?"
	}
	return query
}

func clipSearchColumns() []string {
	return []string{
		"tags",
		"name",
		"search_text",
		"json_extract(COALESCE(metadata_json,'{}'), '$.clean_title')",
		"json_extract(COALESCE(metadata_json,'{}'), '$.clip_summary')",
		"json_extract(COALESCE(metadata_json,'{}'), '$.hook')",
		"json_extract(COALESCE(metadata_json,'{}'), '$.topics')",
		"json_extract(COALESCE(metadata_json,'{}'), '$.speakers')",
		"json_extract(COALESCE(metadata_json,'{}'), '$.mentioned_people')",
		"json_extract(COALESCE(metadata_json,'{}'), '$.people')",
		"json_extract(COALESCE(metadata_json,'{}'), '$.clip_tags')",
		"json_extract(COALESCE(metadata_json,'{}'), '$.search_keywords')",
		"json_extract(COALESCE(metadata_json,'{}'), '$.embedding_text')",
	}
}
