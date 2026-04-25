package handlers

// This file now serves as a reference point. The actual implementations have been
// moved to the following modular files:
// - handler_core.go (ScriptDocsHandler struct, constructor, route registration)
// - handler_generate.go (Generate, GeneratePreview, generate, narrativeOnly, saveScriptToDB, savePreview)
// - script_docs_builder.go (script.BuildScriptDocument and core document building)
// - timeline_utils.go (timeline utility functions)
//
// Import this package to access all script documentation functionality.