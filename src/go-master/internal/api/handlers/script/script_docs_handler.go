package script

// This file now serves as a reference point. The actual implementations have been
// moved to the following modular files:
// - handler_core.go (ScriptDocsHandler struct, constructor, route registration)
// - handler_generate.go (Generate, GeneratePreview, generate, narrativeOnly, saveScriptToDB, savePreview)
// - script_docs_builder.go (BuildScriptDocument and core document building)
// - script_docs_entities.go (entity extraction functions)
// - script_docs_render.go (rendering functions for sections)
// - clip_drive_matching.go (clip drive matching)
// - stock_matching.go (stock folder matching)
// - stock_catalog.go (stock catalog loading)
// - timeline_source_builder.go (timeline building from source text)
// - timeline_render.go (timeline rendering)
// - timeline_types.go (timeline type definitions)
// - timeline_utils.go (timeline utility functions)
//
// Import this package to access all script documentation functionality.