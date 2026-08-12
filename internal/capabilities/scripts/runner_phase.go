package scriptgeneration

func stageSkipped(resumeIdx int, stage Stage) bool {
	return resumeIdx >= 0 && StageIndex(stage) < resumeIdx
}
