package usecase

// minimalTestSchema is a minimal subset of the production schema covering
// all tables the batch flow touches during DB persistence. Declared in a
// _test.go file so it stays out of the public API — no new exported symbols.
const minimalTestSchema = `
	CREATE TABLE scripts (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		topic TEXT NOT NULL DEFAULT '',
		title TEXT NOT NULL DEFAULT '',
		duration INTEGER NOT NULL DEFAULT 0,
		language TEXT NOT NULL DEFAULT 'en',
		template TEXT NOT NULL DEFAULT '',
		mode TEXT NOT NULL DEFAULT '',
		tone TEXT NOT NULL DEFAULT '',
		target_words INTEGER NOT NULL DEFAULT 0,
		final_word_count INTEGER NOT NULL DEFAULT 0,
		status TEXT NOT NULL DEFAULT 'completed',
		narrative_text TEXT,
		timeline_json TEXT,
		entities_json TEXT,
		metadata_json TEXT NOT NULL DEFAULT '{}',
		full_document TEXT,
		model_used TEXT NOT NULL DEFAULT '',
		ollama_base_url TEXT NOT NULL DEFAULT '',
		version INTEGER NOT NULL DEFAULT 1,
		parent_script_id INTEGER,
		is_deleted INTEGER NOT NULL DEFAULT 0,
		created_at TEXT NOT NULL DEFAULT (datetime('now')),
		updated_at TEXT NOT NULL DEFAULT (datetime('now'))
	);

	CREATE TABLE script_sections (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		script_id INTEGER NOT NULL,
		section_type TEXT NOT NULL DEFAULT '',
		section_title TEXT NOT NULL DEFAULT '',
		content TEXT,
		sort_order INTEGER NOT NULL DEFAULT 0,
		word_count INTEGER NOT NULL DEFAULT 0,
		status TEXT NOT NULL DEFAULT 'completed'
	);

	CREATE TABLE script_research_sources (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		script_id INTEGER NOT NULL,
		query TEXT NOT NULL DEFAULT '',
		url TEXT NOT NULL DEFAULT '',
		title TEXT NOT NULL DEFAULT '',
		snippet TEXT NOT NULL DEFAULT '',
		used_in_sections TEXT NOT NULL DEFAULT '[]',
		source_type TEXT NOT NULL DEFAULT 'web',
		created_at TEXT NOT NULL DEFAULT (datetime('now'))
	);

	CREATE TABLE script_outline_sections (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		script_id INTEGER NOT NULL,
		section_index INTEGER NOT NULL DEFAULT 0,
		title TEXT NOT NULL DEFAULT '',
		purpose TEXT NOT NULL DEFAULT '',
		target_words INTEGER NOT NULL DEFAULT 0,
		key_points_json TEXT NOT NULL DEFAULT '[]',
		emotional_role TEXT NOT NULL DEFAULT '',
		created_at TEXT NOT NULL DEFAULT (datetime('now'))
	);

	CREATE TABLE script_generation_logs (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		script_id INTEGER NOT NULL,
		phase TEXT NOT NULL DEFAULT '',
		model TEXT NOT NULL DEFAULT '',
		input_words INTEGER NOT NULL DEFAULT 0,
		output_words INTEGER NOT NULL DEFAULT 0,
		duration_ms INTEGER NOT NULL DEFAULT 0,
		retry_count INTEGER NOT NULL DEFAULT 0,
		cache_status TEXT NOT NULL DEFAULT '',
		prompt_hash TEXT NOT NULL DEFAULT '',
		error TEXT NOT NULL DEFAULT '',
		created_at TEXT NOT NULL DEFAULT (datetime('now'))
	);

	CREATE TABLE script_versions (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		script_id INTEGER NOT NULL,
		version INTEGER NOT NULL DEFAULT 1,
		change_summary TEXT NOT NULL DEFAULT '',
		changed_by TEXT NOT NULL DEFAULT '',
		created_at TEXT NOT NULL DEFAULT (datetime('now'))
	);
`
