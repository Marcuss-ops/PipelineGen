// Package scriptgeneration — builder_docs_default_test.go pins the K1
// contract at the API boundary: Documents are OPT-IN. A payload that OMITS
// the docs block (or states enabled=false explicitly) builds a request with
// documents DISABLED — the code default is off, so payload authors never
// need `"docs": {"enabled": false}` to avoid the Google Docs publish. An
// explicit `{"enabled": true}` still opts in, which is the on-demand
// recovery path for a run whose documents are wanted after the fact.
package scriptgeneration

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

func decodeDocsDefaultEnvelope(t *testing.T, body string) GenerateRequest {
	t.Helper()
	var env scriptpkg.GenerationEnvelopeV2
	decoder := json.NewDecoder(bytes.NewReader([]byte(body)))
	decoder.DisallowUnknownFields()
	require.NoError(t, decoder.Decode(&env), "decode envelope")
	req, err := BuildGenerateRequest(&env, "docs-default-pin")
	require.NoError(t, err)
	return req
}

func docsDefaultEnvelopeBody(docsBlock string) string {
	docs := ""
	if docsBlock != "" {
		docs = `, "docs": ` + docsBlock
	}
	return `{
		"version": 2,
		"items": [{
			"id": "docs-default-pin",
			"title": "Docs default pin",
			"language": "en",
			"source": {"type": "text", "topic": "docs default", "source_text": "docs default body"},
			"output": {"languages": ["en", "pt"]},
			"audio": {"mode": "NONE"}` + docs + `
		}]
	}`
}

// TestBuildGenerateRequest_DocsOmittedDefaultsToDisabled: no docs block in
// the payload → documents DISABLED, while output.languages stay available
// for translation (they are not a docs trigger).
func TestBuildGenerateRequest_DocsOmittedDefaultsToDisabled(t *testing.T) {
	req := decodeDocsDefaultEnvelope(t, docsDefaultEnvelopeBody(""))

	enabled, langs, _ := req.ResolveDocsConfig()
	assert.False(t, enabled, "omitted docs block must default to DISABLED (K1: documents are opt-in)")
	assert.Empty(t, langs)
	assert.False(t, req.Docs.Enabled)

	// The translation fan-out is untouched by the docs default.
	assert.Equal(t, []Language{"en", "pt"}, req.Languages)
}

// TestBuildGenerateRequest_DocsExplicitFalseIsOptOut: an explicit
// enabled=false behaves exactly like the omission — no behavioral delta, so
// payload authors may state it for clarity.
func TestBuildGenerateRequest_DocsExplicitFalseIsOptOut(t *testing.T) {
	req := decodeDocsDefaultEnvelope(t, docsDefaultEnvelopeBody(`{"enabled": false}`))

	enabled, langs, _ := req.ResolveDocsConfig()
	assert.False(t, enabled)
	assert.Empty(t, langs)
}

// TestBuildGenerateRequest_DocsExplicitTrueOptsIn: the on-demand recovery
// path — an explicit enabled=true enables the publication for exactly the
// configured languages.
func TestBuildGenerateRequest_DocsExplicitTrueOptsIn(t *testing.T) {
	req := decodeDocsDefaultEnvelope(t, docsDefaultEnvelopeBody(`{"enabled": true, "languages": ["pt"]}`))

	enabled, langs, _ := req.ResolveDocsConfig()
	assert.True(t, enabled, "explicit docs.enabled=true must opt in")
	assert.Equal(t, []Language{"pt"}, langs)
}
