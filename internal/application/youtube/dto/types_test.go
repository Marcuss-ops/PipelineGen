package dto

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDefaultSegmentPolicy_IsInclusiveFourToSixtySeconds(t *testing.T) {
	policy := DefaultSegmentPolicy()

	require.Equal(t, 4, policy.MinDuration)
	require.Equal(t, 60, policy.MaxDuration)
	require.False(t, policy.ValidDuration(3))
	require.True(t, policy.ValidDuration(4), "the minimum boundary is inclusive")
	require.True(t, policy.ValidDuration(60), "the maximum boundary is inclusive")
	require.False(t, policy.ValidDuration(61))
}

func TestExtractRequest_FlatActorDestinationIsNestedAndExplicitlyFlat(t *testing.T) {
	payload := []byte(`{
		"url": "https://www.youtube.com/watch?v=actor-video",
		"segments": [{"start": "00:01:00", "end": "00:01:10", "name": "actor clip"}],
		"destination": {
			"folder_id": "actor-folder-id",
			"folder_path": "Matt Damon",
			"create_subfolder": false
		}
	}`)

	var req ExtractRequest
	require.NoError(t, json.Unmarshal(payload, &req))
	require.NotNil(t, req.Destination)
	require.Equal(t, "actor-folder-id", req.Destination.FolderID)
	require.Equal(t, "Matt Damon", req.Destination.FolderPath)
	require.False(t, req.Destination.CreateSubfolder)

	encoded, err := json.Marshal(req)
	require.NoError(t, err)
	var wire map[string]any
	require.NoError(t, json.Unmarshal(encoded, &wire))
	destination, ok := wire["destination"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "actor-folder-id", destination["folder_id"])
	require.Equal(t, "Matt Damon", destination["folder_path"])
	require.Equal(t, false, destination["create_subfolder"])
	_, hasTopLevelFolderID := wire["folder_id"]
	_, hasTopLevelFolderPath := wire["folder_path"]
	_, hasTopLevelCreateSubfolder := wire["create_subfolder"]
	require.False(t, hasTopLevelFolderID)
	require.False(t, hasTopLevelFolderPath)
	require.False(t, hasTopLevelCreateSubfolder)
}
