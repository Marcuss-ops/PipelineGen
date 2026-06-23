package books

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"go.uber.org/zap"
)

// ────────────────────────────────────────────────────────────────────
// 3-arm test matrix for the books ProcessBookFromDriveUseCase.
//
//   arm-1 sync validation   : request.Validate()
//   arm-2 sync ok           : Handle() with FakeDriveBookProcessor
//                              returning successful ProcessFromDriveResult
//   arm-3 err mapper        : ProcessBookFromDriveErrMapper standalone +
//                              Handle on nil service → ErrDriveMissing → 503
//
// Drive variant has NO async path (no Async flag in request), so the
// 4-arm matrix from ProcessBookUseCase collapses to 3 arms here.
// Future migrations of sync-only endpoints can copy this shape verbatim.
// ────────────────────────────────────────────────────────────────────

// FakeDriveBookProcessor is the consumer-side fake for the
// driveBookProcessor interface. Zero value returns nil + no error.
type FakeDriveBookProcessor struct {
	Result *ProcessFromDriveResult
	Err    error

	LastRequest *ProcessFromDriveRequest
}

// ProcessBookFromDrive implements driveBookProcessor.
func (f *FakeDriveBookProcessor) ProcessBookFromDrive(_ context.Context, req *ProcessFromDriveRequest) (*ProcessFromDriveResult, error) {
	f.LastRequest = req
	if f.Err != nil {
		return nil, f.Err
	}
	return f.Result, nil
}

// TestProcessBookFromDriveRequest_Validate — arm-1 (sync validation).
func TestProcessBookFromDriveRequest_Validate(t *testing.T) {
	cases := []struct {
		name    string
		req     ProcessBookFromDriveRequest
		wantErr bool
	}{
		{
			name:    "empty — rejected",
			req:     ProcessBookFromDriveRequest{},
			wantErr: true,
		},
		{
			name:    "valid URL — accepted",
			req:     ProcessBookFromDriveRequest{DriveFileURL: "https://drive.google.com/file/d/abc123"},
			wantErr: false,
		},
		{
			name:    "whitespace-only URL — rejected",
			req:     ProcessBookFromDriveRequest{DriveFileURL: "   "},
			wantErr: true,
		},
		{
			name:    "tab-only URL — rejected",
			req:     ProcessBookFromDriveRequest{DriveFileURL: "\t\n"},
			wantErr: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.req.Validate()
			if (err != nil) != tc.wantErr {
				t.Fatalf("wantErr=%v, got=%v", tc.wantErr, err)
			}
		})
	}
}

// TestProcessBookFromDriveUseCase_HandleSyncOK — arm-2 (sync happy path).
//
// The use case projects the result's BookResult fields onto the
// response, plus the voiceover path/link/id/error. Tests assert all
// three branches: nil BookResult, populated BookResult, populated
// VoiceoverPath (with VoiceoverError empty).
func TestProcessBookFromDriveUseCase_HandleSyncOK(t *testing.T) {
	fakeSvc := &FakeDriveBookProcessor{
		Result: &ProcessFromDriveResult{
			Success: true,
			BookResult: &ProcessResult{
				Success:         true,
				OutputPath:      "/tmp/out.md",
				PDFPath:         "/tmp/out.pdf",
				DriveFolderURL:  "https://drive.google.com/folder/abc",
				DriveDocURL:     "https://docs.google.com/doc/abc",
				DrivePDFURL:     "https://drive.google.com/file/abc",
				ChunksProcessed: 7,
				Language:        "en",
			},
			VoiceoverPath:      "/tmp/voiceover.mp3",
			VoiceoverDriveLink: "https://drive.google.com/file/vo",
			VoiceoverDriveID:   "vo-id-xyz",
			VoiceoverError:     "",
		},
	}
	uc := NewProcessBookFromDriveUseCase(fakeSvc, zap.NewNop())

	resp, err := uc.Handle(context.Background(), ProcessBookFromDriveRequest{
		DriveFileURL:      "https://drive.google.com/file/d/abc123",
		GenerateVoiceover: true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !resp.OK || !resp.Success {
		t.Fatalf("expected OK+Success, got: ok=%v success=%v", resp.OK, resp.Success)
	}
	if resp.OutputPath != "/tmp/out.md" || resp.ChunksProcessed != 7 || resp.Language != "en" {
		t.Fatalf("BookResult fields not projected: %+v", resp)
	}
	if resp.VoiceoverPath != "/tmp/voiceover.mp3" || resp.VoiceoverDriveID != "vo-id-xyz" {
		t.Fatalf("voiceover fields not projected: %+v", resp)
	}
	if resp.VoiceoverError != "" {
		t.Fatalf("VoiceoverError should be empty on success, got %q", resp.VoiceoverError)
	}
}

// TestProcessBookFromDriveUseCase_EmptyService — covers ErrDriveMissing.
// When books.Service is nil (or fails to wire at bootstrap), the
// mapper must fire 503 with the consolidated error message.
func TestProcessBookFromDriveUseCase_EmptyService(t *testing.T) {
	uc := NewProcessBookFromDriveUseCase(nil, zap.NewNop())
	_, err := uc.Handle(context.Background(), ProcessBookFromDriveRequest{
		DriveFileURL: "https://drive.google.com/file/d/abc123",
	})
	if !errors.Is(err, ErrDriveMissing) {
		t.Fatalf("want ErrDriveMissing, got %v", err)
	}
	status, msg := ProcessBookFromDriveErrMapper(err)
	if status != http.StatusServiceUnavailable {
		t.Fatalf("status: want 503, got %d (msg=%q)", status, msg)
	}
}

// TestProcessBookFromDriveErrMapper — pin the use-case → HTTP status
// mapping for this endpoint. Mirrors the books ProcessBook mapper
// contract — 503 for missing service, 500 for ErrProcessFailed, 0 for
// unknown. No async paths → no ErrEnqueueFailed / ErrJobsSystem.
func TestProcessBookFromDriveErrMapper(t *testing.T) {
	cases := []struct {
		name       string
		err        error
		wantStatus int
		wantMsgSub string
	}{
		{
			name:       "drive-missing sentinel → 503",
			err:        ErrDriveMissing,
			wantStatus: http.StatusServiceUnavailable,
			wantMsgSub: "books service",
		},
		{
			name:       "ErrProcessFailed → 500 with original message",
			err:        ErrProcessFailed{Message: "book processing failed: file not found"},
			wantStatus: http.StatusInternalServerError,
			wantMsgSub: "book processing failed",
		},
		{
			name:       "unknown error → 0 (delegated to transport default)",
			err:        errors.New("unmapped"),
			wantStatus: 0,
			wantMsgSub: "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			status, msg := ProcessBookFromDriveErrMapper(tc.err)
			if status != tc.wantStatus {
				t.Fatalf("status: want %d, got %d", tc.wantStatus, status)
			}
			if tc.wantMsgSub != "" && !contains(msg, tc.wantMsgSub) {
				t.Fatalf("msg: want substring %q, got %q", tc.wantMsgSub, msg)
			}
		})
	}
}
