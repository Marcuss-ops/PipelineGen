// Package script provides tools for parsing and mapping script-related content.
//
// It handles:
// 1. Script Parsing: Extracting structured data (phrases, metadata) from raw script text.
// 2. Mapping: Correlating script segments with target visual keywords or timestamps.
// 3. Validation: Ensuring generated scripts meet quality and format constraints.
//
// This package is primarily used by the scriptdocs service to refine the outputs of AI models
// before they are processed by the clip association engine.
package script
