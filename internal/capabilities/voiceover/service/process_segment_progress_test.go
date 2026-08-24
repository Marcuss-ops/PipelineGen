package voiceover

import (
	"testing"

	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

func TestSetFinalStageProgressCompletedIncludesUploadAndPersistence(t *testing.T) {
	out := &VoiceoverItemResult{
		Status:      StatusCompleted,
		ErrorCode:   "",
		DriveFileID: "drive-file",
	}
	setFinalStageProgress(out, "it", "child-it")
	for _, stage := range []job.StageName{job.StageVoiceover, job.StageUpload, job.StagePersistence} {
		got, ok := out.StageProgress[string(stage)]
		if !ok || got.Total != 1 || got.Completed != 1 {
			t.Fatalf("stage %s = %+v, want completed=1 total=1", stage, got)
		}
	}
}

func TestSetFinalStageProgressUploadFailureDoesNotClaimPersistence(t *testing.T) {
	out := &VoiceoverItemResult{
		Status:    StatusFailed,
		ErrorCode: string(FailureUpload),
		Error:     "upload failed",
	}
	setFinalStageProgress(out, "it", "child-it")
	if got := out.StageProgress[string(job.StageUpload)]; got.Completed != 0 || got.Total != 1 {
		t.Fatalf("upload stage = %+v, want failed total=1", got)
	}
	if _, ok := out.StageProgress[string(job.StagePersistence)]; ok {
		t.Fatal("persistence must not be reported when upload failed")
	}
}
