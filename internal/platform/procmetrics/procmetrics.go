// Package procmetrics implements the canonical host/process resource
// provider for PipelineGen run telemetry (the platform side of
// capabilities/performance.ResourceObservation).
//
// It reads Linux /proc and sysfs files (process CPU/RSS/swap/I/O, system
// disk utilization/iowait/queue depth, network throughput, thermal zones,
// x86 thermal-throttle counters) and optionally NVIDIA GPUs via
// nvidia-smi. Every source fails soft: a missing file, unreadable sysfs
// entry, or absent GPU leaves that field nil — a missing measurement is
// never represented as a fake zero.
//
// Delta-derived values (CPU%, I/O throughput, disk util, iowait, queue
// depth, network, swap) are measured over the interval since the previous
// Collect call. The first call establishes the baseline and returns a
// sample whose delta fields are nil.
package procmetrics

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"sync"
	"time"

	capperformance "github.com/Marcuss-ops/PipelineGen/internal/capabilities/performance"
)

// Options configures a Provider. Zero values select production defaults.
type Options struct {
	// ProcRoot overrides the /proc mount point (tests).
	ProcRoot string
	// SysRoot overrides the /sys mount point (tests).
	SysRoot string
	// NvidiaSMI is the nvidia-smi binary path. Empty auto-detects via
	// exec.LookPath; "none" disables GPU collection entirely.
	NvidiaSMI string
	// PID is the process to sample. 0 samples the current process.
	PID int
	// Now overrides the clock used for delta windows (tests).
	Now func() time.Time
}

// Provider collects one ResourceObservation per call. It is safe for
// concurrent use; Collect serializes the delta baseline so concurrent run
// loops on the same worker never corrupt the interval math.
type Provider struct {
	procRoot   string
	sysRoot    string
	nvidiaSMI  string
	pid        int
	now        func() time.Time
	ncpu       int
	clockTicks int64
	pageSize   int64

	mu           sync.Mutex
	prev         snapshot
	gpuChecked   bool
	gpuAvailable bool
}

// gpuSample is one nvidia-smi snapshot aggregated across all GPUs.
type gpuSample struct {
	utilPct    float64
	usedBytes  int64
	encoderPct float64
	decoderPct float64
	tempC      float64
}

func New(opts Options) *Provider {
	procRoot := opts.ProcRoot
	if procRoot == "" {
		procRoot = "/proc"
	}
	sysRoot := opts.SysRoot
	if sysRoot == "" {
		sysRoot = "/sys"
	}
	nvidiaSMI := opts.NvidiaSMI
	if nvidiaSMI == "" && nvidiaSMI != "none" {
		if path, err := exec.LookPath("nvidia-smi"); err == nil {
			nvidiaSMI = path
		}
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	// USER_HZ (100) is the fixed clock-tick rate of every mainstream Linux
	// kernel; the stat fields are in these ticks.
	const clockTicks = int64(100)
	return &Provider{
		procRoot:   procRoot,
		sysRoot:    sysRoot,
		nvidiaSMI:  nvidiaSMI,
		pid:        opts.PID,
		now:        now,
		ncpu:       runtime.NumCPU(),
		clockTicks: clockTicks,
		pageSize:   int64(os.Getpagesize()),
	}
}

// Collect returns one resource observation. The identity is carried for
// contract symmetry; the provider is identity-agnostic because it samples
// the worker process the run executes inside.
func (p *Provider) Collect(ctx context.Context, _ capperformance.SampleIdentity) (capperformance.ResourceObservation, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return capperformance.ResourceObservation{}, err
	}
	p.mu.Lock()
	defer p.mu.Unlock()

	cur := p.readSnapshot()
	o := capperformance.ResourceObservation{}
	if cur.rssKB > 0 {
		o.RSSAvgBytes = i64p(cur.rssKB * 1024)
	}
	if cur.hwmKB > 0 {
		o.RSSPeakBytes = i64p(cur.hwmKB * 1024)
	}
	if cur.cpuTempC > 0 {
		o.CPUTempPeakC = f64p(cur.cpuTempC)
	}
	if p.prev.any() {
		o = p.applyDeltas(o, p.prev, cur)
	}
	if gpu := p.readGPU(ctx); gpu != nil {
		o.GPUAvgPct = f64p(gpu.utilPct)
		o.VRAMPeakBytes = i64p(gpu.usedBytes)
		o.EncoderAvgPct = f64p(gpu.encoderPct)
		o.DecoderAvgPct = f64p(gpu.decoderPct)
		o.GPUTempPeakC = f64p(gpu.tempC)
	}
	if cur.throttled {
		t := true
		o.Throttled = &t
	}
	p.prev = cur
	return o, nil
}

// applyDeltas fills the interval-derived fields from the previous snapshot.
func (p *Provider) applyDeltas(o capperformance.ResourceObservation, prev, cur snapshot) capperformance.ResourceObservation {
	wall := cur.wall.Sub(prev.wall).Seconds()
	if wall <= 0 {
		return o
	}
	// Process CPU utilization as a percentage of total machine capacity.
	if cur.cpuTicks >= prev.cpuTicks {
		tickDelta := float64(cur.cpuTicks - prev.cpuTicks)
		pct := tickDelta / (wall * float64(p.clockTicks) * float64(p.ncpu)) * 100
		if pct > 100 {
			pct = 100
		}
		if pct > 0 {
			o.CPUAvgPct = f64p(pct)
		}
	}
	// System-wide iowait share.
	if cur.cpuTotal > prev.cpuTotal && cur.cpuIOWait >= prev.cpuIOWait {
		totalDelta := float64(cur.cpuTotal - prev.cpuTotal)
		iowait := float64(cur.cpuIOWait-prev.cpuIOWait) / totalDelta * 100
		if iowait > 100 {
			iowait = 100
		}
		if iowait > 0 {
			o.IOWaitPct = f64p(iowait)
		}
	}
	// Average disk utilization and queue depth across whole disks.
	if cur.diskCount > 0 && cur.diskDoingMS >= prev.diskDoingMS {
		doingDelta := float64(cur.diskDoingMS - prev.diskDoingMS)
		util := doingDelta / (wall * 1000) / float64(cur.diskCount) * 100
		if util > 100 {
			util = 100
		}
		if util > 0 {
			o.DiskUtilPct = f64p(util)
		}
		if cur.diskWeightedMS >= prev.diskWeightedMS {
			weightedDelta := float64(cur.diskWeightedMS - prev.diskWeightedMS)
			if qd := weightedDelta / (wall * 1000); qd > 0 {
				o.DiskQueueDepth = f64p(qd)
			}
		}
	}
	// Process disk I/O throughput.
	if cur.ioRead >= prev.ioRead {
		if d := cur.ioRead - prev.ioRead; d > 0 {
			o.DiskReadBytes = i64p(int64(d))
		}
	}
	if cur.ioWrite >= prev.ioWrite {
		if d := cur.ioWrite - prev.ioWrite; d > 0 {
			o.DiskWriteBytes = i64p(int64(d))
		}
	}
	// System-wide network throughput (summed, loopback excluded).
	if cur.netRX >= prev.netRX {
		if d := cur.netRX - prev.netRX; d > 0 {
			o.NetworkRXBytes = i64p(int64(d))
		}
	}
	if cur.netTX >= prev.netTX {
		if d := cur.netTX - prev.netTX; d > 0 {
			o.NetworkTXBytes = i64p(int64(d))
		}
	}
	// System-wide swap in/out.
	if cur.swapInPages >= prev.swapInPages {
		if d := cur.swapInPages - prev.swapInPages; d > 0 {
			o.SwapInBytes = i64p(int64(d) * p.pageSize)
		}
	}
	if cur.swapOutPages >= prev.swapOutPages {
		if d := cur.swapOutPages - prev.swapOutPages; d > 0 {
			o.SwapOutBytes = i64p(int64(d) * p.pageSize)
		}
	}
	return o
}

// snapshot holds one raw read of every /proc/sysfs source.
type snapshot struct {
	wall time.Time

	cpuTicks  uint64 // process utime+stime (USER_HZ ticks)
	cpuTotal  uint64 // system-wide total ticks
	cpuIOWait uint64

	rssKB   int64
	hwmKB   int64
	ioRead  int64
	ioWrite int64

	diskDoingMS    uint64
	diskWeightedMS uint64
	diskCount      int

	netRX uint64
	netTX uint64

	swapInPages  uint64
	swapOutPages uint64

	cpuTempC  float64
	throttled bool
}

func (s snapshot) any() bool { return !s.wall.IsZero() }

// readSnapshot reads every source once. Each read fails soft: an unreadable
// file contributes zeros (which the delta math then treats as "no change").
func (p *Provider) readSnapshot() snapshot {
	cur := snapshot{wall: p.now()}
	pid := p.pid
	if pid <= 0 {
		pid = os.Getpid()
	}
	base := filepath.Join(p.procRoot, strconv.Itoa(pid))

	utime, stime := parseProcStat(readFile(base + "/stat"))
	cur.cpuTicks = utime + stime
	rss, hwm := parseProcStatus(readFile(base + "/status"))
	cur.rssKB, cur.hwmKB = rss, hwm
	cur.ioRead, cur.ioWrite = parseProcIO(readFile(base + "/io"))

	total, _, iowait := parseStatCpu(readFirstLine(p.procRoot + "/stat"))
	cur.cpuTotal, cur.cpuIOWait = total, iowait

	rows := parseDiskstats(readFile(p.procRoot + "/diskstats"))
	cur.diskDoingMS, cur.diskWeightedMS, cur.diskCount = aggregateDisks(rows)

	cur.netRX, cur.netTX = parseNetDev(readFile(p.procRoot + "/net/dev"))
	cur.swapInPages, cur.swapOutPages = parseVMStat(readFile(p.procRoot + "/vmstat"))
	cur.cpuTempC = readThermalZones(p.sysRoot)
	cur.throttled = readThrottleCounts(p.sysRoot)
	return cur
}

// readGPU snapshots NVIDIA GPUs via nvidia-smi, permanently disabling GPU
// collection after the first failure (no GPU or missing binary). Returns nil
// when GPUs are unavailable.
func (p *Provider) readGPU(ctx context.Context) *gpuSample {
	if p.nvidiaSMI == "" || p.nvidiaSMI == "none" {
		return nil
	}
	if p.gpuChecked && !p.gpuAvailable {
		return nil
	}
	out, err := exec.CommandContext(ctx, p.nvidiaSMI,
		"--query-gpu=utilization.gpu,memory.used,utilization.encoder,utilization.decoder,temperature.gpu",
		"--format=csv,noheader,nounits").Output()
	if err != nil {
		p.gpuChecked = true
		p.gpuAvailable = false
		return nil
	}
	gpu, ok := parseGPUOutput(string(out))
	if !ok {
		p.gpuChecked = true
		p.gpuAvailable = false
		return nil
	}
	p.gpuChecked = true
	p.gpuAvailable = true
	return &gpu
}
