package system

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeScriptGenerateDeps builds a minimal Service with the supplied
// DB/Jobs checkers so the composite checker has something to call.
func fakeScriptGenerateDeps(dbOK, jobsOK bool) *Service {
	return NewService(ServiceDeps{
		DB:     &staticChecker{check: "db", ok: dbOK},
		Drive:  nil,
		Qdrant: nil,
		Jobs:   &staticChecker{check: "jobs", ok: jobsOK},
	})
}

type staticChecker struct {
	check string
	ok    bool
}

func (c *staticChecker) CheckDB(ctx context.Context) CheckResult {
	return CheckResult{"ok": c.ok, "duration_ms": int64(0)}
}
func (c *staticChecker) CheckDrive(ctx context.Context) CheckResult {
	return CheckResult{"ok": true, "duration_ms": int64(0), "applicable": false}
}
func (c *staticChecker) CheckJobs(ctx context.Context) CheckResult {
	return CheckResult{"ok": c.ok, "duration_ms": int64(0)}
}

func TestCompositeScriptGenerateChecker_AllHealthy(t *testing.T) {
	svc := fakeScriptGenerateDeps(true, true)
	checker := NewScriptGenerateChecker(
		svc,
		&fakeOllamaChecker{ok: true},
		&fakeDriveFolderChecker{ok: true},
		"folder123",
		&fakePublisherChecker{wired: true},
		func() bool { return true },
		func(jobType string) bool { return jobType == "script.generate" },
	)
	checker.SetRouteMounted(func() bool { return true })

	res := checker.CheckScriptGenerate(context.Background())
	assert.True(t, res["ok"].(bool))
	assert.Empty(t, res["error"])
	details := res["details"].(map[string]any)
	assert.NotNil(t, details["db"])
	assert.NotNil(t, details["jobs"])
	assert.NotNil(t, details["script_generate_handler"])
	assert.NotNil(t, details["ollama"])
	assert.NotNil(t, details["drive"])
	assert.NotNil(t, details["document_service"])
	assert.NotNil(t, details["route"])
}

func TestCompositeScriptGenerateChecker_FailsWhenRouteNotMounted(t *testing.T) {
	svc := fakeScriptGenerateDeps(true, true)
	checker := NewScriptGenerateChecker(
		svc,
		&fakeOllamaChecker{ok: true},
		&fakeDriveFolderChecker{ok: true},
		"folder123",
		&fakePublisherChecker{wired: true},
		func() bool { return true },
		func(jobType string) bool { return jobType == "script.generate" },
	)
	checker.SetRouteMounted(func() bool { return false })

	res := checker.CheckScriptGenerate(context.Background())
	assert.False(t, res["ok"].(bool))
	assert.Contains(t, res["error"], "script route not mounted")
}

func TestCompositeScriptGenerateChecker_FailsWhenHandlerMissing(t *testing.T) {
	svc := fakeScriptGenerateDeps(true, true)
	checker := NewScriptGenerateChecker(
		svc,
		&fakeOllamaChecker{ok: true},
		&fakeDriveFolderChecker{ok: true},
		"folder123",
		&fakePublisherChecker{wired: true},
		func() bool { return true },
		func(jobType string) bool { return false },
	)
	checker.SetRouteMounted(func() bool { return true })

	res := checker.CheckScriptGenerate(context.Background())
	assert.False(t, res["ok"].(bool))
	assert.Contains(t, res["error"], "script.generate handler not registered")
}

func TestCompositeScriptGenerateChecker_FailsWhenOllamaDown(t *testing.T) {
	svc := fakeScriptGenerateDeps(true, true)
	checker := NewScriptGenerateChecker(
		svc,
		&fakeOllamaChecker{ok: false, err: errors.New("connection refused")},
		&fakeDriveFolderChecker{ok: true},
		"folder123",
		&fakePublisherChecker{wired: true},
		func() bool { return true },
		func(jobType string) bool { return jobType == "script.generate" },
	)
	checker.SetRouteMounted(func() bool { return true })

	res := checker.CheckScriptGenerate(context.Background())
	assert.False(t, res["ok"].(bool))
	assert.Contains(t, res["error"], "ollama unreachable")
}

func TestCompositeScriptGenerateChecker_FailsWhenDocumentServiceUnavailable(t *testing.T) {
	svc := fakeScriptGenerateDeps(true, true)
	checker := NewScriptGenerateChecker(
		svc,
		&fakeOllamaChecker{ok: true},
		&fakeDriveFolderChecker{ok: true},
		"folder123",
		&fakePublisherChecker{wired: true},
		func() bool { return false },
		func(jobType string) bool { return jobType == "script.generate" },
	)
	checker.SetRouteMounted(func() bool { return true })

	res := checker.CheckScriptGenerate(context.Background())
	assert.False(t, res["ok"].(bool))
	assert.Contains(t, res["error"], "document service not available")
}

func TestCompositeScriptGenerateChecker_FailsWhenDriveFolderInaccessible(t *testing.T) {
	svc := fakeScriptGenerateDeps(true, true)
	checker := NewScriptGenerateChecker(
		svc,
		&fakeOllamaChecker{ok: true},
		&fakeDriveFolderChecker{ok: false, err: errors.New("forbidden")},
		"folder123",
		&fakePublisherChecker{wired: true},
		func() bool { return true },
		func(jobType string) bool { return jobType == "script.generate" },
	)
	checker.SetRouteMounted(func() bool { return true })

	res := checker.CheckScriptGenerate(context.Background())
	assert.False(t, res["ok"].(bool))
	assert.Contains(t, res["error"], "drive folder inaccessible")
}

func TestCompositeScriptGenerateChecker_NilServiceFails(t *testing.T) {
	checker := NewScriptGenerateChecker(
		nil,
		&fakeOllamaChecker{ok: true},
		&fakeDriveFolderChecker{ok: true},
		"folder123",
		&fakePublisherChecker{wired: true},
		func() bool { return true },
		func(jobType string) bool { return jobType == "script.generate" },
	)
	checker.SetRouteMounted(func() bool { return true })

	res := checker.CheckScriptGenerate(context.Background())
	assert.False(t, res["ok"].(bool))
	assert.Contains(t, res["error"], "health service not wired")
}

func TestCompositeScriptGenerateChecker_NilOllamaFails(t *testing.T) {
	checker := NewScriptGenerateChecker(
		fakeScriptGenerateDeps(true, true),
		nil,
		&fakeDriveFolderChecker{ok: true},
		"folder123",
		&fakePublisherChecker{wired: true},
		func() bool { return true },
		func(jobType string) bool { return jobType == "script.generate" },
	)
	checker.SetRouteMounted(func() bool { return true })

	res := checker.CheckScriptGenerate(context.Background())
	assert.False(t, res["ok"].(bool))
	assert.Contains(t, res["error"], "ollama checker not wired")
}

func TestCompositeScriptGenerateChecker_NilCheckerOptedOut(t *testing.T) {
	rc := NewReadyChecker(fakeScriptGenerateDeps(true, true))
	resp := rc.CheckReady(context.Background())
	require.Contains(t, resp.Checks, "script_generate")
	assert.True(t, resp.Checks["script_generate"]["ok"].(bool))
	assert.False(t, resp.Checks["script_generate"]["applicable"].(bool))
}

type fakeOllamaChecker struct {
	ok  bool
	err error
}

func (f *fakeOllamaChecker) CheckOllama(ctx context.Context) error {
	if f.ok {
		return nil
	}
	if f.err != nil {
		return f.err
	}
	return errors.New("ollama down")
}

type fakeDriveFolderChecker struct {
	ok  bool
	err error
}

func (f *fakeDriveFolderChecker) CheckFolder(ctx context.Context, folderID string) error {
	if f.ok {
		return nil
	}
	if f.err != nil {
		return f.err
	}
	return errors.New("drive folder check failed")
}

type fakePublisherChecker struct {
	wired bool
}

func (f *fakePublisherChecker) IsWired() bool { return f.wired }
