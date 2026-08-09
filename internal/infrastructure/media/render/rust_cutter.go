package render

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"

	stockpipeline "github.com/Marcuss-ops/PipelineGen/internal/application/assets/providers/stock/stockpipeline"
	"go.uber.org/zap"
)

type rustCutRequest struct {
	Operation  string       `json:"operation"`
	FFmpegPath string       `json:"ffmpeg_path,omitempty"`
	SourcePath string       `json:"source_path"`
	Jobs       []rustCutJob `json:"jobs"`
	Codec      string       `json:"codec,omitempty"`
	Preset     string       `json:"preset,omitempty"`
	CRF        int          `json:"crf,omitempty"`
	NoAudio    bool         `json:"no_audio,omitempty"`
}

type rustCutJob struct {
	JobID      string  `json:"job_id"`
	StartSec   float64 `json:"start_sec"`
	EndSec     float64 `json:"end_sec"`
	OutputPath string  `json:"output_path"`
}

type rustCutResponse struct {
	OK         bool          `json:"ok"`
	Operation  string        `json:"operation"`
	SourcePath string        `json:"source_path"`
	Items      []rustCutItem `json:"items"`
	Error      string        `json:"error"`
}

type rustCutItem struct {
	JobID       string  `json:"job_id"`
	OutputPath  string  `json:"output_path"`
	Status      string  `json:"status"`
	SizeBytes   int64   `json:"size_bytes"`
	DurationSec float64 `json:"duration_sec"`
	Error       string  `json:"error"`
}

type rustMusclesRunner interface {
	Run(context.Context, string, []byte) ([]byte, error)
}

type execRustMusclesRunner struct{}

func (execRustMusclesRunner) Run(ctx context.Context, binary string, input []byte) ([]byte, error) {
	cmd := exec.CommandContext(ctx, binary)
	cmd.Stdin = bytes.NewReader(input)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if message := stderr.String(); message != "" {
			return nil, fmt.Errorf("rust muscles: %w: %s", err, message)
		}
		return nil, fmt.Errorf("rust muscles: %w", err)
	}
	return stdout.Bytes(), nil
}

// RustCutter adapts the capability-scoped Rust media executor to the
// application-owned VideoCutter port. It deliberately exchanges file paths
// and technical media options only; Go remains responsible for lifecycle and
// canonical state.
type RustCutter struct {
	binaryPath string
	ffmpegPath string
	log        *zap.Logger
	runner     rustMusclesRunner
}

func NewRustCutter(binaryPath, ffmpegPath string, log *zap.Logger) *RustCutter {
	if log == nil {
		log = zap.NewNop()
	}
	if ffmpegPath == "" {
		ffmpegPath = "ffmpeg"
	}
	return &RustCutter{
		binaryPath: binaryPath,
		ffmpegPath: ffmpegPath,
		log:        log,
		runner:     execRustMusclesRunner{},
	}
}

var _ stockpipeline.VideoCutter = (*RustCutter)(nil)

func (c *RustCutter) Cut(ctx context.Context, req stockpipeline.CutRequest) (stockpipeline.CutBatchResult, error) {
	result := stockpipeline.CutBatchResult{SourcePath: req.SourcePath, Items: make([]stockpipeline.CutItemResult, len(req.Jobs))}
	for i, job := range req.Jobs {
		result.Items[i] = stockpipeline.CutItemResult{JobID: job.OutputPath, OutputPath: job.OutputPath, Status: stockpipeline.CutItemStatusUnknown}
	}

	wireReq := rustCutRequest{
		Operation: "cut_batch", FFmpegPath: c.ffmpegPath, SourcePath: req.SourcePath,
		Codec: req.Codec, Preset: req.Preset, CRF: req.CRF, NoAudio: req.NoAudio,
		Jobs: make([]rustCutJob, len(req.Jobs)),
	}
	for i, job := range req.Jobs {
		wireReq.Jobs[i] = rustCutJob{JobID: job.OutputPath, StartSec: job.StartSec, EndSec: job.EndSec, OutputPath: job.OutputPath}
	}
	payload, err := json.Marshal(wireReq)
	if err != nil {
		return result, fmt.Errorf("marshal rust cut request: %w", err)
	}
	output, err := c.runner.Run(ctx, c.binaryPath, append(payload, '\n'))
	if err != nil {
		return result, err
	}
	var response rustCutResponse
	if err := json.Unmarshal(bytes.TrimSpace(output), &response); err != nil {
		return result, fmt.Errorf("decode rust cut response: %w", err)
	}
	if response.Operation != "cut_batch" {
		return result, fmt.Errorf("rust muscles returned operation %q", response.Operation)
	}

	byJob := make(map[string]rustCutItem, len(response.Items))
	for _, item := range response.Items {
		byJob[item.JobID] = item
	}
	for i, job := range req.Jobs {
		item, ok := byJob[job.OutputPath]
		if !ok {
			result.Items[i].Status = stockpipeline.CutItemStatusFailed
			result.Items[i].Err = fmt.Errorf("rust muscles omitted job %q", job.OutputPath)
			continue
		}
		result.Items[i].OutputPath = item.OutputPath
		result.Items[i].SizeBytes = item.SizeBytes
		if (item.Status != "succeeded" && item.Status != "validated") || item.OutputPath == "" {
			result.Items[i].Status = stockpipeline.CutItemStatusFailed
			result.Items[i].Err = fmt.Errorf("rust cut failed: %s", item.Error)
			continue
		}
		result.Items[i].DurationSec = item.DurationSec
		result.Items[i].Status = stockpipeline.CutItemStatusValidated
		if size, hash, hashErr := hashCutOutput(item.OutputPath); hashErr == nil {
			result.Items[i].SizeBytes = size
			result.Items[i].SHA256Hex = hash
		} else {
			result.Items[i].Status = stockpipeline.CutItemStatusFailed
			result.Items[i].Err = fmt.Errorf("validate rust cut output: %w", hashErr)
		}
	}
	if !response.OK {
		return result, fmt.Errorf("rust cut batch failed: %s", response.Error)
	}
	return result, nil
}

func hashCutOutput(path string) (int64, string, error) {
	file, err := os.Open(path)
	if err != nil {
		return 0, "", err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || info.Size() == 0 {
		if err == nil {
			err = io.ErrUnexpectedEOF
		}
		return 0, "", err
	}
	digest := sha256.New()
	if _, err := io.Copy(digest, file); err != nil {
		return 0, "", err
	}
	return info.Size(), hex.EncodeToString(digest.Sum(nil)), nil
}
