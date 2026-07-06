// Package voiceover — destination_subfolder_test.go
//
// Azione #1 (July 2026): destinationStage + finalizeStage removed from Service.
// The subfolder creation + persistence behavior is now covered by
// ProcessSegmentUseCase.Execute tests. This test is skipped until the
// equivalent ProcessSegmentUseCase-based test ships.
package voiceover

import (
	"testing"
)

func TestDestinationStageCreatesAndPersistsSubfolder(t *testing.T) {
	t.Skip("Azione #1 (July 2026): destinationStage + finalizeStage removed from Service — behavior now covered by ProcessSegmentUseCase.Execute")
}
