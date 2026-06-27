// Package workerstats is the canonical Linux /proc sampler that produces
// *job.WorkerHardwareStats POJOs for the worker heartbeat + admin
// cert-report endpoints.
//
// Scope (RW-PROD-013, June 2026):
//   - Sampling POJO  : internal/domain/job.WorkerHardwareStats
//   - Sampling impl  : Sample(ctx, Config) here
//   - Metric vars    : internal/infrastructure/observability.Worker*
//   - Metric emit    : heartbeat handler (caller-side bridge, NOT here)
//
// Boundary contract: pkg/workerstats MUST NOT import
// prometheus/client_golang or any of the global observability surfaces.
// Metric definitions live in internal/infrastructure/observability/ —
// the caller (heartbeat handler) bridges the POJO into metric values
// field-by-field. This separation keeps pkg/ leaf-only, isolates the
// /proc pulling concern from the metric-emission concern, and lets
// tests run without a Prometheus DefaultGatherer.
//
// Units rule (forward-looking): the POJO's godoc is authoritative;
// this sampler follows it verbatim. Raw bytes for storage/network;
// 0.0-1.0 for ratios; no seconds-vs-bytes confusion. Drift between
// sampler's units documentation and the POJO godoc is a
// reading-order hazard for operators; the POJO godoc wins.
package workerstats

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/job"
)

// Config selects which paths /proc-style source files the sampler reads,
// plus the network device filter and disk mount path. All fields are
// optional; zero values default to system canonical paths
// (/proc/stat, /proc/net/dev, "/" for Statfs).
//
// Config is what tests override to point the sampler at hand-written
// fixture files. Pass the zero value for production.
type Config struct {
	// CPUSourcePath is the /proc/stat-equivalent file. Empty = /proc/stat.
	CPUSourcePath string
	// NetworkSourcePath is the /proc/net/dev-equivalent file. Empty = /proc/net/dev.
	NetworkSourcePath string
	// NetworkDevice filters /proc/net/dev lines (e.g. "eth0"). Empty = sum ALL devices.
	NetworkDevice string
	// DiskMountPath is the path syscall.Statfs is called on. Empty = "/".
	DiskMountPath string
}

func (c Config) cpuPath() string {
	if c.CPUSourcePath != "" {
		return c.CPUSourcePath
	}
	return "/proc/stat"
}

func (c Config) netPath() string {
	if c.NetworkSourcePath != "" {
		return c.NetworkSourcePath
	}
	return "/proc/net/dev"
}

func (c Config) diskPath() string {
	if c.DiskMountPath != "" {
		return c.DiskMountPath
	}
	return "/"
}

// Sample reads all four sources (CPU, network, disk, memory) and
// returns the canonical POJO. The read is best-effort: a single failed
// source yields a partial POJO + a joined error so callers can log the
// anomaly and decide whether to ship zero values or omit telemetry.
// The sampler NEVER panics; /proc paths that fail to exist on a
// non-Linux system yield error wrapped around the partial result.
//
// Returned POJO is never nil — even when ALL sources fail, the
// returned struct is initialized to zero values and the error carries
// the failure reasons. This guarantees callers can blindly project
// fields without a nil check.
func Sample(ctx context.Context, cfg Config) (*job.WorkerHardwareStats, error) {
	if cfg.cpuPath() == "" || cfg.netPath() == "" || cfg.diskPath() == "" {
		return nil, errors.New("workerstats: empty source path after Config defaults")
	}

	out := &job.WorkerHardwareStats{SampledAtUnixMs: nowUnixMs()}

	var errs []error

	if ratio, err := sampleCPU(cfg.cpuPath()); err != nil {
		errs = append(errs, fmt.Errorf("cpu: %w", err))
	} else {
		out.CPUUsageRatio = ratio
	}

	if rx, tx, err := sampleNetwork(cfg.netPath(), cfg.NetworkDevice); err != nil {
		errs = append(errs, fmt.Errorf("network: %w", err))
	} else {
		out.NetRxBytes = rx
		out.NetTxBytes = tx
	}

	if free, used, err := sampleDisk(cfg.diskPath()); err != nil {
		errs = append(errs, fmt.Errorf("disk: %w", err))
	} else {
		out.DiskFreeBytes = free
		out.DiskUsedBytes = used
	}

	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	out.MemoryAllocBytes = ms.Alloc
	out.MemorySysBytes = ms.Sys
	out.MemoryHeapBytes = ms.HeapAlloc
	out.MemoryNumGC = ms.NumGC

	if len(errs) > 0 {
		return out, errors.Join(errs...)
	}
	return out, nil
}

// nowUnixMs is a tiny helper kept package-private so tests can pin a
// deterministic timestamp via the Config seam if needed. The reference
// clock is time.Now() (wall time) — monotonic time is irrelevant for
// telemetry, and UnixMilli keeps the POJO field stable across servers
// with different reservation clocks.
func nowUnixMs() int64 {
	return time.Now().UnixMilli()
}

// ── internal helpers ──────────────────────────────────────────────

// sampleCPU parses the first line of /proc/stat with the "cpu " prefix
// and returns (user+nice+system) / (busy + idle) as a 0.0-1.0 ratio.
// Lines after the first (per-CPU "cpu0", "cpu1", ...) are deliberately
// ignored — aggregate busy ratio for the heartbeat fan-out, not per-CPU
// detail.
//
// Numerator = user + nice + system (the canonical "doing work" slice).
// Denominator = numerator + idle. iowait / irq / softirq / steal /
// guest slices are intentionally EXCLUDED from both: they reflect
// conditions the operator can read in /proc/stat directly and would
// otherwise drive the ratio to noisy extremes (iowait-dominated hosts
// report low busy ratio despite appearing sluggish). The units /
// derivation contract is canonically documented on the POJO's
// CPUUsageRatio godoc (see internal/domain/job.WorkerHardwareStats);
// this comment is a pointer, not the source of truth.
func sampleCPU(path string) (float32, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "cpu ") {
			continue
		}
		fields := strings.Fields(line)
		// /proc/stat cpu line: user nice system idle iowait irq softirq steal guest guest_nice
		if len(fields) < 5 {
			return 0, fmt.Errorf("workerstats: short /proc/stat cpu line (%d fields)", len(fields))
		}
		user, _ := strconv.ParseUint(fields[1], 10, 64)
		nice, _ := strconv.ParseUint(fields[2], 10, 64)
		system, _ := strconv.ParseUint(fields[3], 10, 64)
		idle, _ := strconv.ParseUint(fields[4], 10, 64)
		total := user + nice + system + idle
		if total == 0 {
			return 0, nil
		}
		busy := user + nice + system
		return float32(busy) / float32(total), nil
	}
	if err := sc.Err(); err != nil {
		return 0, err
	}
	return 0, errors.New("workerstats: no 'cpu ' aggregate line in /proc/stat")
}

// sampleNetwork reads /proc/net/dev and returns (rx, tx) byte totals.
// NetworkDevice filter (e.g. "eth0") restricts the sum to a single
// interface; empty filter sums ALL non-loopback interfaces. The
// lo-exclusion is mandatory in BOTH branches — loopback traffic is
// operationally noise for the heartbeat metric.
//
// The function MUTUALLY EXCLUDES (rx, tx, nil) on an empty-filter scan
// that finds no non-loopback interface: it returns a sentinel error
// instead. Container/edge sandboxes (pre-up network namespace,
// cgroup-restricted workers) commonly have only lo; silently returning
// zeros hides a real "we have no carrier" anomaly from operators.
func sampleNetwork(path, device string) (uint64, uint64, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, 0, err
	}
	defer f.Close()

	var totalRx, totalTx uint64
	sc := bufio.NewScanner(f)
	// Skip the two header lines ("Inter-|...Receive..." and
	// "face |..." columns).
	headerLines := 0
	matched := false
	for sc.Scan() {
		line := sc.Text()
		if headerLines < 2 {
			headerLines++
			continue
		}
		// /proc/net/dev format:
		// "<if>: <rx_bytes> <rx_packets> ... <tx_bytes> <tx_packets> ..."
		colon := strings.Index(line, ":")
		if colon < 0 {
			continue
		}
		iface := strings.TrimSpace(line[:colon])
		if device != "" && iface != device {
			continue
		}
		if iface == "lo" {
			continue
		}
		fields := strings.Fields(line[colon+1:])
		if len(fields) < 9 {
			continue
		}
		// rx_bytes=fields[0], tx_bytes=fields[8].
		rx, _ := strconv.ParseUint(fields[0], 10, 64)
		tx, _ := strconv.ParseUint(fields[8], 10, 64)
		totalRx += rx
		totalTx += tx
		matched = true
	}
	if err := sc.Err(); err != nil {
		return 0, 0, err
	}
	if !matched {
		// Both filters require a hit: explicit device OR at least one
		// non-loopback interface. The all-lo host case (device empty,
		// only lo present in /proc/net/dev) now returns a sentinel
		// error rather than (0, 0, nil) — race-condition-safe
		// regression lock per Wave 22 PR-5 polish round 3.
		if device != "" {
			return 0, 0, fmt.Errorf("workerstats: device %q not found in %s", device, path)
		}
		return 0, 0, fmt.Errorf("workerstats: no non-loopback interface found in %s", path)
	}
	return totalRx, totalTx, nil
}

// sampleDisk issues syscall.Statfs on path and returns (free, used)
// in bytes. Total = free + used + reserved (we surface free/used;
// reserved is collapsed into used — operators reading the POJO see
// the operationally meaningful split, not the kernel-internal one).
func sampleDisk(path string) (uint64, uint64, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0, 0, err
	}
	// Frsize is the fundamental block size (may differ from Bsize).
	bsize := uint64(st.Bsize)
	if st.Frsize > 0 {
		bsize = uint64(st.Frsize)
	}
	free := st.Bavail * bsize
	total := st.Blocks * bsize
	if total < free {
		return 0, 0, fmt.Errorf("workerstats: Statfs reported total < free (%d < %d)", total, free)
	}
	return free, total - free, nil
}
