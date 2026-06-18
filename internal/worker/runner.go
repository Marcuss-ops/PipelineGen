package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/core/domain/job"
)

type Runner struct {
	broker      job.Broker
	registry    *Registry
	workspace   *Workspace
	assetClient AssetClient
	log         *zap.Logger
	workerID    string
	sessionID   string
	caps        []string
}

func NewRunner(broker job.Broker, registry *Registry, workspace *Workspace, assetClient AssetClient, log *zap.Logger, workerID, sessionID string, caps []string) *Runner {
	return &Runner{
		broker:      broker,
		registry:    registry,
		workspace:   workspace,
		assetClient: assetClient,
		log:         log,
		workerID:    workerID,
		sessionID:   sessionID,
		caps:        caps,
	}
}

func (r *Runner) Run(ctx context.Context) error {
	if r.registry == nil {
		r.registry = NewRegistry()
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		lease, err := r.broker.Claim(ctx, job.ClaimCommand{
			WorkerID:        r.workerID,
			WorkerSessionID: r.sessionID,
			Capabilities:    r.caps,
			WaitSeconds:     20,
		})
		if err != nil {
			r.log.Warn("claim failed", zap.Error(err))
			time.Sleep(2 * time.Second)
			continue
		}
		if lease == nil || lease.Job == nil {
			continue
		}
		if err := r.runLease(ctx, lease); err != nil {
			r.log.Warn("job failed", zap.String("job_id", lease.Job.ID), zap.Error(err))
		}
	}
}

func (r *Runner) runLease(parent context.Context, lease *job.Lease) error {
	jobCtx, cancel := context.WithCancel(parent)
	defer cancel()

	jobDir, err := r.workspace.Prepare(lease.Job.ID)
	if err != nil {
		return r.fail(jobCtx, lease, err)
	}
	defer func() {
		_ = r.workspace.Cleanup(lease.Job.ID)
	}()

	tools := NewTools(r.broker, r.workerID, r.sessionID, lease.Job, jobDir, r.assetClient)

	if assets := ParseInputAssets(lease.Job.Payload); len(assets) > 0 {
		for i, assetID := range assets {
			if _, err := tools.DownloadAsset(jobCtx, assetID); err != nil {
				return r.fail(jobCtx, lease, fmt.Errorf("download asset %d (%s): %w", i, assetID, err))
			}
			_ = tools.Progress(jobCtx, 5+i, "staged input asset")
		}
	}

	handlerResult, err := r.registry.Dispatch(jobCtx, lease.Job, tools)
	if err != nil {
		return r.fail(jobCtx, lease, err)
	}

	resultJSON, err := json.Marshal(handlerResult)
	if err != nil {
		return r.fail(jobCtx, lease, err)
	}
	if err := r.uploadOutputs(jobCtx, lease.Job.ID, handlerResult); err != nil {
		return r.fail(jobCtx, lease, err)
	}
	return r.broker.Complete(jobCtx, job.CompleteCommand{
		WorkerID:         r.workerID,
		WorkerSessionID:  r.sessionID,
		JobID:            lease.Job.ID,
		LeaseID:          lease.LeaseID,
		ExpectedRevision: lease.Job.Revision,
		Result:           resultJSON,
	})
}

func (r *Runner) fail(ctx context.Context, lease *job.Lease, err error) error {
	return r.broker.Fail(ctx, job.FailCommand{
		WorkerID:         r.workerID,
		WorkerSessionID:  r.sessionID,
		JobID:            lease.Job.ID,
		LeaseID:          lease.LeaseID,
		ExpectedRevision: lease.Job.Revision,
		Error:            err.Error(),
	})
}

func (r *Runner) uploadOutputs(ctx context.Context, jobID string, handlerResult map[string]any) error {
	if r.assetClient == nil || len(handlerResult) == 0 {
		return nil
	}
	type outputFile struct {
		assetID string
		path    string
	}
	var files []outputFile
	seen := make(map[string]struct{})
	add := func(assetID, path string) {
		assetID = strings.TrimSpace(assetID)
		path = strings.TrimSpace(path)
		if path == "" {
			return
		}
		if assetID == "" {
			assetID = jobID + ":" + filepath.Base(path)
		}
		key := assetID + "|" + path
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		files = append(files, outputFile{assetID: assetID, path: path})
	}

	for _, key := range []string{"output_path", "pdf_path", "markdown_path"} {
		if v, ok := handlerResult[key].(string); ok {
			add(jobID+":"+key, v)
		}
	}

	if raw, ok := handlerResult["output_files"]; ok {
		switch list := raw.(type) {
		case []string:
			for _, path := range list {
				add("", path)
			}
		case []any:
			for i, item := range list {
				switch v := item.(type) {
				case string:
					add("", v)
				case map[string]any:
					path, _ := v["path"].(string)
					assetID, _ := v["asset_id"].(string)
					if assetID == "" {
						assetID = fmt.Sprintf("%s:output_files:%d", jobID, i)
					}
					add(assetID, path)
				}
			}
		}
	}

	for _, file := range files {
		if _, err := os.Stat(file.path); err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return err
		}
		if err := r.assetClient.UploadFile(ctx, file.assetID, file.path); err != nil {
			return fmt.Errorf("upload output %s: %w", file.path, err)
		}
	}
	return nil
}
