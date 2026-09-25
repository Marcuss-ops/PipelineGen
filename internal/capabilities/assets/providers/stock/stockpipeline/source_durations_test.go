// Package stockpipeline — source_durations_test.go.
//
// The caller-supplied source durations travel four boundaries before the
// planner reads them (HTTP request → StockCommand → jobs payload wire →
// StockRunPayload → RunInput). Each boundary takes its own copy, and a
// request without durations must keep the pre-existing wire shape (no
// "source_durations" key at all). These tests pin both properties, because a
// dropped or aliased map here silently re-introduces the serial provider
// probe the field exists to remove.
package stockpipeline

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

const (
	durationURL1 = "https://www.youtube.com/watch?v=aaaaaaaaaaa"
	durationURL2 = "https://www.youtube.com/watch?v=bbbbbbbbbbb"
)

func TestSourceDurations_TravelFromRequestToJobPayload(t *testing.T) {
	request := &StockSearchAndRunRequest{
		DirectURLs:      []string{durationURL1, durationURL2},
		SourceDurations: map[string]float64{durationURL1: 640, durationURL2: 900},
	}

	cmd, err := FromSearchAndRunRequest(request)
	require.NoError(t, err)
	require.Equal(t, map[string]float64{durationURL1: 640, durationURL2: 900}, cmd.SourceDurations)

	// Boundary copy: mutating the request map after conversion must not
	// reach the command.
	request.SourceDurations[durationURL1] = 1
	require.Equal(t, float64(640), cmd.SourceDurations[durationURL1])

	payload := cmd.ToJobPayload()
	require.Equal(t, map[string]float64{durationURL1: 640, durationURL2: 900}, payload["source_durations"])

	// The jobs wire shape must decode back into the worker's payload type.
	raw, err := json.Marshal(payload)
	require.NoError(t, err)
	var decoded StockRunPayload
	require.NoError(t, json.Unmarshal(raw, &decoded))
	require.Equal(t, map[string]float64{durationURL1: 640, durationURL2: 900}, decoded.SourceDurations)

	// The sync path carries the same values onto RunInput.
	input := cmd.ToRunInput()
	require.Equal(t, map[string]float64{durationURL1: 640, durationURL2: 900}, input.SourceDurations)
}

func TestSourceDurations_CopiesAreIndependent(t *testing.T) {
	cmd := &StockCommand{
		DirectURLs:      []string{durationURL1},
		SourceDurations: map[string]float64{durationURL1: 300},
	}

	payload := cmd.ToJobPayload()
	wireDurations, ok := payload["source_durations"].(map[string]float64)
	require.True(t, ok)
	wireDurations[durationURL1] = 7
	require.Equal(t, float64(300), cmd.SourceDurations[durationURL1])

	input := cmd.ToRunInput()
	input.SourceDurations[durationURL1] = 11
	require.Equal(t, float64(300), cmd.SourceDurations[durationURL1])
}

func TestSourceDurations_AbsentKeepsLegacyWireShape(t *testing.T) {
	cmd := &StockCommand{DirectURLs: []string{durationURL1}}

	payload := cmd.ToJobPayload()
	_, present := payload["source_durations"]
	require.False(t, present, "a run without supplied durations must not emit the key")

	require.Nil(t, cmd.ToRunInput().SourceDurations)
	require.Nil(t, copySourceDurations(nil))
	require.Nil(t, copySourceDurations(map[string]float64{}))
}
