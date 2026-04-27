package cron

import (
	"context"
	"fmt"
	"io/ioutil"
	"net/http"
	"strings"
	"time"

	"go.uber.org/zap"
	"velox/go-master/internal/repository/harvester"
)

// HarvesterCronService manages persistent harvester cron jobs
type HarvesterCronService struct {
	repo       *harvester.Repository
	log        *zap.Logger
	apiURL     string
	termsFile  string
	stopCh     chan struct{}
	jobs       map[string]*harvester.Job
}

// NewHarvesterCronService creates a new harvester cron service
func NewHarvesterCronService(repo *harvester.Repository, log *zap.Logger, apiURL, termsFile string) *HarvesterCronService {
	return &HarvesterCronService{
		repo:      repo,
		log:       log,
		apiURL:    apiURL,
		termsFile: termsFile,
		stopCh:    make(chan struct{}),
		jobs:      make(map[string]*harvester.Job),
	}
}

// Start begins the harvester cron service
func (s *HarvesterCronService) Start(ctx context.Context) {
	s.log.Info("Starting harvester cron service")

	// Load existing jobs from DB
	s.loadJobsFromDB(ctx)

	// Main loop - check every minute for jobs to run
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			s.checkAndRunJobs(ctx)
		case <-s.stopCh:
			s.log.Info("Harvester cron service stopped")
			return
		case <-ctx.Done():
			s.log.Info("Harvester cron service stopped via context")
			return
		}
	}
}

// Stop stops the harvester cron service
func (s *HarvesterCronService) Stop() {
	close(s.stopCh)
}

// loadJobsFromDB loads all enabled jobs from the database
func (s *HarvesterCronService) loadJobsFromDB(ctx context.Context) {
	jobs, err := s.repo.ListJobs(ctx)
	if err != nil {
		s.log.Error("Failed to load jobs from DB", zap.Error(err))
		return
	}

	for _, job := range jobs {
		if job.Enabled {
			s.jobs[job.ID] = job
		}
	}

	s.log.Info("Loaded harvester jobs from DB", zap.Int("count", len(s.jobs)))
}

// checkAndRunJobs checks for jobs that need to run
func (s *HarvesterCronService) checkAndRunJobs(ctx context.Context) {
	jobsToRun, err := s.repo.GetJobsToRun(ctx)
	if err != nil {
		s.log.Error("Failed to get jobs to run", zap.Error(err))
		return
	}

	for _, job := range jobsToRun {
		s.log.Info("Running harvester job", zap.String("name", job.Name), zap.String("id", job.ID))
		go s.runJob(ctx, job)
	}
}

// runJob executes a single harvester job
func (s *HarvesterCronService) runJob(ctx context.Context, job *harvester.Job) {
	// Build the request payload
	payload := fmt.Sprintf(`{"topic":"%s","duration":80,"languages":["en"],"template":"documentary"}`, job.Query)

	// Make request to the API
	url := s.apiURL + "/api/script-docs/generate"
	resp, err := http.Post(url, "application/json", strings.NewReader(payload))
	if err != nil {
		s.log.Error("Failed to run harvester job", zap.String("job", job.Name), zap.Error(err))
		return
	}
	defer resp.Body.Close()

	body, _ := ioutil.ReadAll(resp.Body)
	s.log.Info("Harvester job completed",
		zap.String("job", job.Name),
		zap.Int("status", resp.StatusCode),
		zap.String("response", string(body)))

	// Update job run times in DB
	if err := s.repo.UpdateJobRunTimes(ctx, job.ID); err != nil {
		s.log.Error("Failed to update job run times", zap.Error(err))
	}
}

// AddJob adds a new job and starts scheduling it
func (s *HarvesterCronService) AddJob(ctx context.Context, job *harvester.Job) error {
	if err := s.repo.CreateJob(ctx, job); err != nil {
		return err
	}

	if job.Enabled {
		s.jobs[job.ID] = job
	}

	s.log.Info("Added harvester job", zap.String("name", job.Name), zap.String("id", job.ID))
	return nil
}

// RemoveJob removes a job and stops scheduling it
func (s *HarvesterCronService) RemoveJob(ctx context.Context, id string) error {
	if err := s.repo.DeleteJob(ctx, id); err != nil {
		return err
	}

	delete(s.jobs, id)

	s.log.Info("Removed harvester job", zap.String("id", id))
	return nil
}

// ToggleJob enables or disables a job
func (s *HarvesterCronService) ToggleJob(ctx context.Context, id string) error {
	if err := s.repo.ToggleJob(ctx, id); err != nil {
		return err
	}

	job, err := s.repo.GetJob(ctx, id)
	if err != nil {
		return err
	}

	if job.Enabled {
		s.jobs[id] = job
	} else {
		delete(s.jobs, id)
	}

	s.log.Info("Toggled harvester job", zap.String("id", id), zap.Bool("enabled", job.Enabled))
	return nil
}

// ListJobs returns all jobs
func (s *HarvesterCronService) ListJobs(ctx context.Context) ([]*harvester.Job, error) {
	return s.repo.ListJobs(ctx)
}
