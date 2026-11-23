package gc

// resume.go owns retention for the content-addressed resume documents written
// by clip.render submit. The CAS is shared with media assets, so this pass is a
// conservative mark-and-sweep:
//
//   - only a blob that parses as a valid ResumeDocument is a candidate;
//   - a candidate referenced by a non-terminal job payload is kept;
//   - only candidates older than the retention are deleted.
//
// A false positive (a 64-hex token in an active payload) keeps a file; there is
// no false negative for a live resume reference, because every active job
// payload is scanned for every 64-hex address it contains.

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
)

// maxResumeDocumentBytes bounds what the parser is allowed to read. Media
// assets are normally far larger and are skipped without being loaded.
const maxResumeDocumentBytes = 4 << 20

// ResumeConfig is the CAS resume-retention pass. Parse must return true only
// for a structurally valid resume document; ActiveRefs is the set of addresses
// referenced by non-terminal jobs.
type ResumeConfig struct {
	Root       string
	Retention  time.Duration
	ActiveRefs map[string]struct{}
	Parse      func([]byte) (bool, error)
}

// ResumeReport is the bounded inventory and action taken for resume documents.
type ResumeReport struct {
	Path          string    `json:"path"`
	Retention     string    `json:"retention"`
	Cutoff        time.Time `json:"cutoff"`
	Files         int64     `json:"files"`
	Bytes         int64     `json:"bytes"`
	ActiveFiles   int64     `json:"active_files"`
	EligibleFiles int64     `json:"eligible_files"`
	EligibleBytes int64     `json:"eligible_bytes"`
	DeletedFiles  int64     `json:"deleted_files"`
	DeletedBytes  int64     `json:"deleted_bytes"`
	Errors        []string  `json:"errors,omitempty"`
}

var casAddressPattern = regexp.MustCompile(`(?i)[0-9a-f]{64}`)

// RefsInJSONPayloads extracts every content address mentioned by job payloads.
// Scanning all 64-hex tokens is deliberately conservative: a plan digest or an
// artifact hash also lands in the keep set, while the resume digest of an
// active settle job can never be missed.
func RefsInJSONPayloads(payloads []string) map[string]struct{} {
	refs := make(map[string]struct{})
	for _, payload := range payloads {
		for _, match := range casAddressPattern.FindAllString(payload, -1) {
			refs[strings.ToLower(match)] = struct{}{}
		}
	}
	return refs
}

func pruneResumeCAS(ctx context.Context, cfg ResumeConfig, now time.Time, apply bool) (*ResumeReport, error) {
	if strings.TrimSpace(cfg.Root) == "" {
		return nil, fmt.Errorf("%w: resume CAS root is empty", ErrInvalidConfig)
	}
	if cfg.Retention <= 0 {
		return nil, fmt.Errorf("%w: resume retention must be > 0", ErrInvalidConfig)
	}
	if cfg.Parse == nil {
		return nil, fmt.Errorf("%w: resume parser is required", ErrInvalidConfig)
	}
	if cfg.ActiveRefs == nil {
		return nil, fmt.Errorf("%w: active resume references are required (fail closed)", ErrInvalidConfig)
	}
	abs, err := filepath.Abs(cfg.Root)
	if err != nil {
		return nil, fmt.Errorf("%w: resolve resume CAS root: %v", ErrInvalidConfig, err)
	}
	report := &ResumeReport{
		Path:      abs,
		Retention: cfg.Retention.String(),
		Cutoff:    now.Add(-cfg.Retention).UTC(),
	}
	info, err := os.Stat(abs)
	if err != nil {
		if os.IsNotExist(err) {
			return report, nil
		}
		return nil, fmt.Errorf("%w: stat resume CAS root %q: %v", ErrInvalidConfig, abs, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("%w: resume CAS root %q is not a directory", ErrInvalidConfig, abs)
	}

	var dirs []string
	walkErr := filepath.WalkDir(abs, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			report.Errors = append(report.Errors, path+": "+walkErr.Error())
			return nil
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		if d.IsDir() {
			dirs = append(dirs, path)
			return nil
		}
		if d.Type()&fs.ModeSymlink != 0 {
			return nil
		}
		name := d.Name()
		if !digest.IsCanonicalSHA256(name) {
			return nil
		}
		fi, err := d.Info()
		if err != nil {
			report.Errors = append(report.Errors, path+": "+err.Error())
			return nil
		}
		if !fi.Mode().IsRegular() || fi.Size() <= 0 || fi.Size() > maxResumeDocumentBytes {
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			report.Errors = append(report.Errors, path+": "+err.Error())
			return nil
		}
		isResume, err := cfg.Parse(raw)
		if err != nil {
			report.Errors = append(report.Errors, path+": "+err.Error())
			return nil
		}
		if !isResume {
			return nil
		}
		report.Files++
		report.Bytes += fi.Size()
		if _, active := cfg.ActiveRefs[name]; active {
			report.ActiveFiles++
			return nil
		}
		if !fi.ModTime().Before(report.Cutoff) {
			return nil
		}
		report.EligibleFiles++
		report.EligibleBytes += fi.Size()
		if !apply {
			return nil
		}
		if err := os.Remove(path); err != nil {
			report.Errors = append(report.Errors, path+": "+err.Error())
			return nil
		}
		report.DeletedFiles++
		report.DeletedBytes += fi.Size()
		return nil
	})
	if walkErr != nil {
		if errors.Is(walkErr, context.Canceled) || errors.Is(walkErr, context.DeadlineExceeded) {
			return report, walkErr
		}
		report.Errors = append(report.Errors, walkErr.Error())
	}
	if apply {
		pruneEmptyDirs(abs, dirs)
	}
	return report, nil
}
