package script

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/app/knobs"
	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/drive"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/media/rustexec"
	pgmedia "github.com/Marcuss-ops/PipelineGen/internal/platform/postgres/media"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/remotejob"
)

// remote_final_job_config.go — adapter construction and the operator knobs
// (worker pin, queue budget, attach budget) the hand-off reads at startup.

const defaultFinalJobMasterURL = "https://veloxmanager.duckdns.org"

// defaultFinalJobAttachBudget keeps script.generate attached through the usual
// remote composite render. The old 60s default marked the local job FAILED while
// the Master was still rendering for 6-14 minutes; those renders continued, but
// their completed result was never joined back to the parent job. Leave enough
// room for the observed render times while remaining below the 60m script job
// timeout. Operators can still override this with
// VELOX_FINAL_JOB_ATTACH_SECONDS.
const defaultFinalJobAttachBudget = 50 * time.Minute

// deprecatedFinalJobWorker51ID is the pin this build used to apply when no
// operator override was configured. Worker 51 was quarantined on 2026-11-02, so
// a hardcoded default silently routed every handoff to an unhealthy worker: r9
// sat PENDING for 43 minutes against it. The pin is now opt-in
// (VELOX_FINAL_JOB_WORKER_ID); with no override, the Master places the job.
const deprecatedFinalJobWorker51ID = "velox-worker-523925eb"

// finalJobWorkerPin is the placement pin for a handoff, or "" when the Master
// must place the job itself. There is deliberately no default: an operator pin
// is a deployment fact (which worker is healthy), not something this binary can
// know. The retired worker-51 default is rejected loudly instead of silently
// resurrected by a stale environment file.
func finalJobWorkerPin() (string, error) {
	pin := strings.TrimSpace(knobs.String("VELOX_FINAL_JOB_WORKER_ID"))
	if pin == "" {
		return "", nil
	}
	if pin == deprecatedFinalJobWorker51ID {
		return "", fmt.Errorf("VELOX_FINAL_JOB_WORKER_ID pins the retired worker %s; unset it to let the Master place the job", pin)
	}
	return pin, nil
}

// finalJobQueueBudget reads VELOX_FINAL_JOB_QUEUE_SECONDS: how long a handoff
// may wait for the Master to lease and START the job before the wait is handed
// back. It bounds the failure mode r9 exposed (46 minutes in PENDING, then a
// cancellation): a queued job cannot finish, so holding the local worker for
// the whole render budget buys nothing. 0 disables the queue budget. A
// malformed value is a configuration error, not a silent default.
func finalJobQueueBudget() (time.Duration, error) {
	raw := strings.TrimSpace(knobs.String("VELOX_FINAL_JOB_QUEUE_SECONDS"))
	if raw == "" {
		return defaultFinalJobQueueBudget, nil
	}
	seconds, err := strconv.Atoi(raw)
	if err != nil || seconds < 0 {
		return 0, fmt.Errorf("VELOX_FINAL_JOB_QUEUE_SECONDS must be a non-negative integer number of seconds, got %q", raw)
	}
	return time.Duration(seconds) * time.Second, nil
}

// defaultFinalJobQueueBudget is generous for a healthy Master (r101 leased in
// 0 s, r103 in 58 s) and far below the 46-minute stall that motivated it.
const defaultFinalJobQueueBudget = 3 * time.Minute

// finalJobAttachBudget reads VELOX_FINAL_JOB_ATTACH_SECONDS. 0 disables the
// hand-off (every wait runs to completion: the pre-split blocking contract).
// A malformed value is a configuration error, not a silent default.
func finalJobAttachBudget() (time.Duration, error) {
	raw := strings.TrimSpace(knobs.String("VELOX_FINAL_JOB_ATTACH_SECONDS"))
	if raw == "" {
		return defaultFinalJobAttachBudget, nil
	}
	seconds, err := strconv.Atoi(raw)
	if err != nil || seconds < 0 {
		return 0, fmt.Errorf("VELOX_FINAL_JOB_ATTACH_SECONDS must be a non-negative integer number of seconds, got %q", raw)
	}
	return time.Duration(seconds) * time.Second, nil
}

type RemoteFinalJobAdapter struct {
	media     *pgmedia.MediaSearcher
	stock     drive.Reader
	client    *remotejob.Client
	segments  scriptgen.FinalJobVideoSegmentPreparer
	preflight finalJobMediaPreflighter
	// artifactToken is the Master admin bearer used only for the final
	// artifact GET. It is deliberately separate from the M2M token in
	// client.Token: the M2M credential submits jobs, while artifact download
	// is an operator-scoped read (`velox download-final` uses the same one).
	artifactToken string
	// attachBudget bounds a single attempt's wait on the Master render; see
	// defaultFinalJobAttachBudget and finalJobAttachBudget.
	attachBudget time.Duration
	// queueBudget bounds the NOT-STARTED portion of that wait; see
	// defaultFinalJobQueueBudget and finalJobQueueBudget. It is carried on the
	// transport client so the resume path shares the same rule.
	queueBudget time.Duration
	// resolverNS is the accumulated runtime-asset resolution time reported in
	// the receipt as handoff_resolve_ms (diagnostics only).
	resolverNS atomic.Int64
	// resolved caches one handoff's runtime-asset resolution by identity. The
	// payload builder asks for the SAME source once per timeline window (85
	// windows over ~13 sources in r50, 65,1 s of resolution); publishing and
	// signing those bytes again per window answers a question the identity has
	// already answered. Cleared when a new handoff starts, never shared across
	// runs, and only ever stores a reference to immutable content.
	resolvedMu sync.Mutex
	resolved   map[string]map[string]any
	hits       int
	// resolvedRunID scopes the cache to ONE run. An earlier attempt of the SAME
	// run keeps what was already resolved; a different run (or a retry reusing
	// this adapter) must never inherit another run's identities.
	resolvedRunID string
	// inflight is the single-flight index over identities currently being
	// resolved. Two timeline windows can ask for the same source, and two
	// folders may offer the same file; the second caller waits for the first
	// instead of downloading and publishing those bytes again. Entries hold
	// immutable content, so a waiter always receives the same answer the
	// builder produced.
	inflightMu sync.Mutex
	inflight   map[string]*finalJobResolveCall
}

type finalJobMediaPreflighter interface {
	Preflight(context.Context, rustexec.PreflightRequest) (rustexec.PreflightDecision, error)
}

// SetMediaPreflighter installs the Rust mediaexec.v2 validator used before a
// final-job PREPARE. The compiled plan is already sealed here, so the request
// checks the exact track windows that will be sent to the Master.
func (a *RemoteFinalJobAdapter) SetMediaPreflighter(preflighter finalJobMediaPreflighter) {
	if a != nil {
		a.preflight = preflighter
	}
}

// finalJobSegmentHandoffTracer is the optional handoff-scoped timing surface of
// the fragment preparer. It lives here, not in the capability port, because the
// scope is a wiring detail: the capability receives only the resulting block.
// A preparer that cannot report fragment phases leaves the block nil instead of
// claiming zero work.
type finalJobSegmentHandoffTracer interface {
	BeginHandoff(context.Context) (context.Context, uint64)
	Timing(scope uint64) scriptgen.RemoteFinalJobSegmentPrepTiming
}

// SetVideoSegmentPreparer installs the local fragment certifier for
// Producer-owned clips and overlays. The payload builder explicitly skips
// stock-pool slots, so this component never lists or opens stock Drive files.
func (a *RemoteFinalJobAdapter) SetVideoSegmentPreparer(preparer scriptgen.FinalJobVideoSegmentPreparer) {
	if a != nil {
		a.segments = preparer
	}
}

// finalJobResolveCall is one in-progress resolution. `done` is closed exactly
// once, after ref/err are written, so a waiter needs no further lock.
type finalJobResolveCall struct {
	done chan struct{}
	ref  map[string]any
	err  error
}

// RemoteFinalJobConfig contains the resolved service dependencies. The app
// composition root owns ComposeRoot traversal; this package owns the handoff.
type RemoteFinalJobConfig struct {
	Media        *pgmedia.MediaSearcher
	Stock        drive.Reader
	Client       *remotejob.Client
	AttachBudget time.Duration
	QueueBudget  time.Duration
	// ArtifactToken is the bearer for the final artifact GET. Empty disables
	// automatic download; the finalizer then fails closed instead of silently
	// completing a final job without its video.
	ArtifactToken string
}

func NewRemoteFinalJobAdapter(cfg RemoteFinalJobConfig) (*RemoteFinalJobAdapter, error) {
	if cfg.Media == nil || cfg.Stock == nil || cfg.Client == nil {
		return nil, fmt.Errorf("remote final-job adapter requires media search, Drive stock reader, and client")
	}
	if cfg.AttachBudget <= 0 {
		cfg.AttachBudget = defaultFinalJobAttachBudget
	}
	if cfg.QueueBudget < 0 {
		return nil, fmt.Errorf("remote final-job queue budget cannot be negative")
	}
	cfg.Client.QueueTimeout = cfg.QueueBudget
	return &RemoteFinalJobAdapter{media: cfg.Media, stock: cfg.Stock, client: cfg.Client, attachBudget: cfg.AttachBudget, queueBudget: cfg.QueueBudget, artifactToken: strings.TrimSpace(cfg.ArtifactToken)}, nil
}

func (a *RemoteFinalJobAdapter) BaseURL() string {
	if a == nil || a.client == nil {
		return ""
	}
	return a.client.BaseURL
}

func NewRemoteFinalJobAdapterFromEnvironment(media *pgmedia.MediaSearcher, stock drive.Reader) (*RemoteFinalJobAdapter, error) {
	if media == nil || stock == nil {
		return nil, fmt.Errorf("remote final-job handoff requires the media SSOT and Drive stock reader")
	}
	if strings.TrimSpace(os.Getenv("OBJECTSTORE_PUBLIC_URL")) == "" {
		return nil, fmt.Errorf("remote final-job HTTPS handoff requires OBJECTSTORE_PUBLIC_URL; only stock assets may use Drive")
	}
	token := strings.TrimSpace(knobs.String("VELOX_FINAL_JOB_M2M_SECRET"))
	if token == "" {
		path := strings.TrimSpace(knobs.String("VELOX_FINAL_JOB_CREDENTIALS_FILE"))
		if path == "" {
			home, _ := os.UserHomeDir()
			path = filepath.Join(home, "creator-77-master.env")
		}
		var err error
		token, err = remotejob.ReadTokenFile(path)
		if err != nil {
			return nil, err
		}
	}
	base := strings.TrimSpace(knobs.String("VELOX_FINAL_JOB_MASTER_URL"))
	if base == "" {
		base = defaultFinalJobMasterURL
	}
	masterURL, err := url.Parse(base)
	if err != nil || masterURL == nil || masterURL.Scheme != "https" || masterURL.Host == "" {
		return nil, fmt.Errorf("remote final-job master URL must use HTTPS")
	}
	budget, err := finalJobAttachBudget()
	if err != nil {
		return nil, err
	}
	queueBudget, err := finalJobQueueBudget()
	if err != nil {
		return nil, err
	}
	artifactToken := strings.TrimSpace(knobs.String("VELOX_ARTIFACT_TOKEN"))
	if artifactToken == "" {
		artifactToken = strings.TrimSpace(knobs.String("VELOX_MASTER_ADMIN_TOKEN"))
	}
	return NewRemoteFinalJobAdapter(RemoteFinalJobConfig{
		Media: media, Stock: stock, Client: remotejob.New(base, token),
		AttachBudget: budget, QueueBudget: queueBudget, ArtifactToken: artifactToken,
	})
}
