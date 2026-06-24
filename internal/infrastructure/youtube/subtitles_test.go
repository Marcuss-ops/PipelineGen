package youtube

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const rollingVTTFixture = `WEBVTT
Kind: captions
Language: en

00:00:00.000 --> 00:00:02.000
Hello world

00:00:02.000 --> 00:00:04.000
this is a test

00:00:04.000 --> 00:00:06.000
is a test of the rolling

00:00:06.000 --> 00:00:08.500
of the rolling subtitle system

00:00:10.000 --> 00:00:12.000
next chapter starts here
`

func writeVTTFixture(t *testing.T, name, body string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, name)
	require.NoError(t, os.WriteFile(p, []byte(body), 0o644))
	return p
}

func TestParseVTTFile_AllCues_WhenWindowIsZero(t *testing.T) {
	p := writeVTTFixture(t, "rolling.vtt", rollingVTTFixture)
	got, err := ParseVTTFile(p, 0, 0)
	require.NoError(t, err)
	assert.NotEmpty(t, got)
}

func TestParseVTTFile_WindowFilter(t *testing.T) {
	p := writeVTTFixture(t, "rolling.vtt", rollingVTTFixture)
	got, err := ParseVTTFile(p, 5.5, 9.0)
	require.NoError(t, err)
	// Window 5.5–9.0 keeps the cue at 6→8.5 and partial coverage of the
	// 4→6 cue, but NOT the 0–2 and 10–12 cues. After dedup + stripCueOverlap
	// the rolling block collapses to the tail fragment.
	assert.Contains(t, got, "subtitle system")
	assert.NotContains(t, got, "Hello world")
	assert.NotContains(t, got, "next chapter")
}

func TestParseVTTFile_EmptyBodyReturnsEmpty(t *testing.T) {
	p := writeVTTFixture(t, "empty.vtt", "WEBVTT\n\n")
	got, err := ParseVTTFile(p, 0, 0)
	require.NoError(t, err)
	assert.Equal(t, "", got)
}

func TestParseVTTFile_NonExistentPathFails(t *testing.T) {
	_, err := ParseVTTFile(filepath.Join(t.TempDir(), "missing.vtt"), 0, 0)
	require.Error(t, err)
}

func TestStripCueOverlap_RemovesFourWordOverlap(t *testing.T) {
	prev := "is a test of the rolling"
	curr := "of the rolling subtitle system"
	got := stripCueOverlap(prev, curr)
	assert.Equal(t, "subtitle system", got)
}

func TestStripCueOverlap_NoOverlap(t *testing.T) {
	prev := "completely different"
	curr := "next chapter begins"
	got := stripCueOverlap(prev, curr)
	assert.Equal(t, "next chapter begins", got)
}

func TestStripCueOverlap_OneWordOverlapNotStripped(t *testing.T) {
	prev := "the rolling"
	curr := "the subtitle"
	got := stripCueOverlap(prev, curr)
	assert.Equal(t, "the subtitle", got)
}

func TestStripCueOverlap_EmptyInputsArePassthrough(t *testing.T) {
	assert.Equal(t, "hello", stripCueOverlap("", "hello"))
	assert.Equal(t, "", stripCueOverlap("", ""))
}

func TestCollapseRepeatedSections_RemovesDuplicates(t *testing.T) {
	got := collapseRepeatedSections("first part >> first part >> second part")
	assert.Equal(t, "first part >> second part", got)
}

func TestCollapseRepeatedSections_NoOp(t *testing.T) {
	in := "everything is fine here"
	got := collapseRepeatedSections(in)
	assert.Equal(t, in, got)
}

func TestCollapseImmediateWordRepetitions_RemovesImmediateDups(t *testing.T) {
	got := collapseImmediateWordRepetitions("hello hello world")
	assert.Equal(t, "hello world", got)
}

func TestCollapseImmediateWordRepetitions_KeepsNonAdjacentDups(t *testing.T) {
	in := "hello world hello"
	got := collapseImmediateWordRepetitions(in)
	assert.Equal(t, "hello world hello", got)
}

func TestCollapseImmediateWordRepetitions_ShortInputPassthrough(t *testing.T) {
	in := "hi"
	got := collapseImmediateWordRepetitions(in)
	assert.Equal(t, "hi", got)
}
