package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	appjobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	domainjob "github.com/Marcuss-ops/PipelineGen/internal/domain/job"
)

type AssetClient interface {
	Download(ctx context.Context, assetID string) (io.ReadCloser, string, error)
	UploadFile(ctx context.Context, assetID, filePath string) error
}

type Tools struct {
	broker      appjobs.Broker
	workerID    string
	sessionID   string
	jobID       string
	leaseID     string
	revision    int
	workspace   string
	assetClient AssetClient
}

func NewTools(broker appjobs.Broker, workerID, sessionID string, j *domainjob.Job, workspace string, assetClient AssetClient) *Tools {
	return &Tools{
		broker:      broker,
		workerID:    workerID,
		sessionID:   sessionID,
		jobID:       j.ID,
		leaseID:     j.LeaseID,
		revision:    j.Revision,
		workspace:   workspace,
		assetClient: assetClient,
	}
}

func (t *Tools) Progress(ctx context.Context, progress int, message string) error {
	return t.broker.Progress(ctx, appjobs.ProgressCommand{
		WorkerID:         t.workerID,
		WorkerSessionID:  t.sessionID,
		JobID:            t.jobID,
		LeaseID:          t.leaseID,
		ExpectedRevision: t.revision,
		Progress:         progress,
		Message:          message,
	})
}

func (t *Tools) IsCancelled(ctx context.Context) (bool, error) {
	return t.broker.IsCancelled(ctx, t.jobID, t.leaseID)
}

func (t *Tools) Renew(ctx context.Context, leaseTTL time.Duration) error {
	lease, err := t.broker.Renew(ctx, appjobs.RenewCommand{
		WorkerID:         t.workerID,
		WorkerSessionID:  t.sessionID,
		JobID:            t.jobID,
		LeaseID:          t.leaseID,
		ExpectedRevision: t.revision,
		LeaseTTL:         leaseTTL,
	})
	if err != nil {
		return err
	}
	if lease != nil && lease.Job != nil {
		t.revision = lease.Job.Revision
	}
	return nil
}

func (t *Tools) DownloadAsset(ctx context.Context, assetID string) (string, error) {
	if t.assetClient == nil {
		return "", fmt.Errorf("asset client not configured")
	}
	rc, filename, err := t.assetClient.Download(ctx, assetID)
	if err != nil {
		return "", err
	}
	defer rc.Close()
	if filename == "" {
		filename = assetID
	}
	dst := filepath.Join(t.workspace, "input", filename)
	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
		return "", err
	}
	f, err := os.Create(dst)
	if err != nil {
		return "", err
	}
	defer f.Close()
	if _, err := io.Copy(f, rc); err != nil {
		return "", err
	}
	return dst, nil
}

func ParseInputAssets(payload json.RawMessage) []string {
	var raw struct {
		InputAssets []struct {
			AssetID string `json:"asset_id"`
		} `json:"input_assets"`
	}
	if len(payload) == 0 {
		return nil
	}
	if err := json.Unmarshal(payload, &raw); err != nil {
		return nil
	}
	out := make([]string, 0, len(raw.InputAssets))
	for _, a := range raw.InputAssets {
		if a.AssetID != "" {
			out = append(out, a.AssetID)
		}
	}
	return out
}
