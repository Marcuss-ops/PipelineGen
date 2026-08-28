package procmetrics

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	capperformance "github.com/Marcuss-ops/PipelineGen/internal/capabilities/performance"
)

func TestParseProcStat(t *testing.T) {
	// comm contains spaces and parens; utime=field 14, stime=field 15
	// (token index 11/12 after the state field).
	content := "123 (proc (with) parens) S 1 2 3 4 5 6 7 8 9 10 1000 2000 15 16 17"
	utime, stime := parseProcStat(content)
	if utime != 1000 || stime != 2000 {
		t.Fatalf("utime=%d stime=%d, want 1000/2000", utime, stime)
	}
	if u, s := parseProcStat("garbage"); u != 0 || s != 0 {
		t.Fatalf("malformed stat parsed as %d/%d", u, s)
	}
}

func TestParseProcStatus(t *testing.T) {
	content := "Name:\tworker\nVmPeak:\t  200 kB\nVmSize:\t  180 kB\nVmHWM:\t  150 kB\nVmRSS:\t  120 kB\nVmSwap:\t  5 kB\n"
	rss, hwm := parseProcStatus(content)
	if rss != 120 || hwm != 150 {
		t.Fatalf("rss=%d hwm=%d, want 120/150", rss, hwm)
	}
}

func TestParseProcIO(t *testing.T) {
	content := "rchar: 100\nwchar: 50\nread_bytes: 12345\nwrite_bytes: 6789\ncancelled_write_bytes: 1\n"
	read, write := parseProcIO(content)
	if read != 12345 || write != 6789 {
		t.Fatalf("read=%d write=%d, want 12345/6789", read, write)
	}
}

func TestParseStatCpu(t *testing.T) {
	line := "cpu  100 20 30 400 50 6 7 8 0 0"
	total, idle, iowait := parseStatCpu(line)
	if total != 621 || idle != 400 || iowait != 50 {
		t.Fatalf("total=%d idle=%d iowait=%d, want 621/400/50", total, idle, iowait)
	}
	if t2, _, _ := parseStatCpu("mem  1 2"); t2 != 0 {
		t.Fatalf("non-cpu line parsed: %d", t2)
	}
}

func TestAggregateDisksExcludesPartitions(t *testing.T) {
	content := `   8       0 sda 100 10 8000 500 200 20 16000 700 0 1200 3000
   8       1 sda1 50 5 4000 250 100 10 8000 350 0 600 1500
 259       0 nvme0n1 10 0 800 40 5 0 400 30 0 70 200
 259       1 nvme0n1p1 5 0 400 20 2 0 200 15 0 35 100
`
	doing, weighted, count := aggregateDisks(parseDiskstats(content))
	if count != 2 {
		t.Fatalf("whole-disk count=%d, want 2 (partitions excluded)", count)
	}
	if doing != 1270 || weighted != 3200 {
		t.Fatalf("doing=%d weighted=%d, want 1270/3200", doing, weighted)
	}
}

func TestParseNetDevExcludesLoopback(t *testing.T) {
	content := `Inter-|   Receive                                                |  Transmit
 face |bytes    packets errs drop fifo frame compressed multicast|bytes    packets errs drop fifo colls carrier compressed
    lo: 1000 10 0 0 0 0 0 0 1000 10 0 0 0 0 0 0
  eth0: 5000 20 0 0 0 0 0 0 9000 30 0 0 0 0 0 0
`
	rx, tx := parseNetDev(content)
	if rx != 5000 || tx != 9000 {
		t.Fatalf("rx=%d tx=%d, want 5000/9000", rx, tx)
	}
}

func TestParseVMStat(t *testing.T) {
	content := "pswpin 42\npswpout 7\n"
	in, out := parseVMStat(content)
	if in != 42 || out != 7 {
		t.Fatalf("in=%d out=%d, want 42/7", in, out)
	}
}

func TestParseGPUOutput(t *testing.T) {
	content := "62, 1234, 40, 55, 63\n80, 2048, 60, 70, 71\n"
	sample, ok := parseGPUOutput(content)
	if !ok {
		t.Fatal("expected a parseable GPU sample")
	}
	if sample.utilPct != 80 || sample.encoderPct != 60 || sample.decoderPct != 70 || sample.tempC != 71 {
		t.Fatalf("sample=%+v", sample)
	}
	wantBytes := int64((1234 + 2048) * 1024 * 1024)
	if sample.usedBytes != wantBytes {
		t.Fatalf("usedBytes=%d, want %d", sample.usedBytes, wantBytes)
	}
	if _, ok := parseGPUOutput("no gpu here\n"); ok {
		t.Fatal("garbage must not parse as a GPU sample")
	}
}

// writeProcFixtures lays out a fake /proc tree for a fixed pid.
func writeProcFixtures(t *testing.T, root string, pid int) {
	t.Helper()
	dir := filepath.Join(root, "proc", itoa(pid))
	mustMkdirAll(t, filepath.Join(dir))
	mustWrite(t, filepath.Join(dir, "stat"), "42 (worker) S 0 1 1 0 -1 4194304 0 0 0 0 100 200 0 0 20 0 1 0")
	mustWrite(t, filepath.Join(dir, "status"), "VmPeak:\t  200 kB\nVmSize:\t  180 kB\nVmHWM:\t  150 kB\nVmRSS:\t  120 kB\nVmSwap:\t  0 kB\n")
	mustWrite(t, filepath.Join(dir, "io"), "rchar: 100\nwchar: 50\nread_bytes: 1000\nwrite_bytes: 500\n")
	mustWrite(t, filepath.Join(root, "proc", "stat"), "cpu  1000 20 30 400 50 6 7 8 0 0")
	mustWrite(t, filepath.Join(root, "proc", "diskstats"), "   8       0 sda 100 10 8000 500 200 20 16000 700 0 500 1000\n")
	mustWrite(t, filepath.Join(root, "proc", "net", "dev"), "Inter-| Receive\n    lo: 1000 10 0 0 0 0 0 0 1000 10 0 0 0 0 0 0\n  eth0: 1000 20 0 0 0 0 0 0 500 30 0 0 0 0 0 0\n")
	mustWrite(t, filepath.Join(root, "proc", "vmstat"), "pswpin 10\npswpout 5\n")
	mustWrite(t, filepath.Join(root, "sys", "class", "thermal", "thermal_zone0", "temp"), "45000\n")
}

// bumpProcFixtures raises every counter so the second Collect sees deltas.
func bumpProcFixtures(t *testing.T, root string, pid int) {
	t.Helper()
	dir := filepath.Join(root, "proc", itoa(pid))
	mustWrite(t, filepath.Join(dir, "stat"), "42 (worker) S 0 1 1 0 -1 4194304 0 0 0 0 200 300 0 0 20 0 1 0")
	mustWrite(t, filepath.Join(dir, "status"), "VmPeak:\t  260 kB\nVmSize:\t  240 kB\nVmHWM:\t  180 kB\nVmRSS:\t  200 kB\nVmSwap:\t  0 kB\n")
	mustWrite(t, filepath.Join(dir, "io"), "rchar: 200\nwchar: 100\nread_bytes: 3000\nwrite_bytes: 900\n")
	mustWrite(t, filepath.Join(root, "proc", "stat"), "cpu  1950 20 30 400 100 6 7 8 0 0")
	mustWrite(t, filepath.Join(root, "proc", "diskstats"), "   8       0 sda 100 10 8000 500 200 20 16000 700 0 1500 3000\n")
	mustWrite(t, filepath.Join(root, "proc", "net", "dev"), "Inter-| Receive\n    lo: 1000 10 0 0 0 0 0 0 1000 10 0 0 0 0 0 0\n  eth0: 3000 20 0 0 0 0 0 0 800 30 0 0 0 0 0 0\n")
	mustWrite(t, filepath.Join(root, "proc", "vmstat"), "pswpin 12\npswpout 5\n")
	mustWrite(t, filepath.Join(root, "sys", "class", "thermal", "thermal_zone0", "temp"), "52000\n")
}

func TestCollectComputesIntervalDeltas(t *testing.T) {
	root := t.TempDir()
	writeProcFixtures(t, root, 42)

	now := time.Unix(1000, 0)
	p := New(Options{ProcRoot: filepath.Join(root, "proc"), SysRoot: filepath.Join(root, "sys"), NvidiaSMI: "none", PID: 42, Now: func() time.Time { return now }})

	// First call: baseline — only instantaneous fields, no deltas.
	first, err := p.Collect(context.Background(), capperformance.SampleIdentity{RunID: "run-1", JobID: "job-1"})
	if err != nil {
		t.Fatal(err)
	}
	if first.RSSAvgBytes == nil || *first.RSSAvgBytes != 120*1024 {
		t.Fatalf("baseline rss=%v, want %d", first.RSSAvgBytes, 120*1024)
	}
	if first.CPUAvgPct != nil || first.DiskReadBytes != nil {
		t.Fatalf("baseline must not carry deltas: %+v", first)
	}

	// Second call one second later with bumped counters.
	now = now.Add(time.Second)
	bumpProcFixtures(t, root, 42)
	o, err := p.Collect(context.Background(), capperformance.SampleIdentity{RunID: "run-1", JobID: "job-1"})
	if err != nil {
		t.Fatal(err)
	}

	// Process CPU ticks delta = (200+300)-(100+200) = 200 over 1s wall.
	wantCPU := 200 / (1 * 100 * float64(runtime.NumCPU())) * 100
	if o.CPUAvgPct == nil || diff(*o.CPUAvgPct, wantCPU) > 0.01 {
		t.Fatalf("cpu=%v, want ~%v", o.CPUAvgPct, wantCPU)
	}
	// iowait delta 50 over total delta 1000 → 5%.
	if o.IOWaitPct == nil || diff(*o.IOWaitPct, 5) > 0.01 {
		t.Fatalf("iowait=%v, want 5", o.IOWaitPct)
	}
	// Disk util: 1000ms doing over 1s wall on 1 disk → 100 (capped).
	if o.DiskUtilPct == nil || *o.DiskUtilPct != 100 {
		t.Fatalf("disk util=%v, want 100", o.DiskUtilPct)
	}
	// Queue depth: 2000ms weighted over 1s wall → 2.
	if o.DiskQueueDepth == nil || diff(*o.DiskQueueDepth, 2) > 0.01 {
		t.Fatalf("queue depth=%v, want 2", o.DiskQueueDepth)
	}
	if o.DiskReadBytes == nil || *o.DiskReadBytes != 2000 {
		t.Fatalf("disk read=%v, want 2000", o.DiskReadBytes)
	}
	if o.DiskWriteBytes == nil || *o.DiskWriteBytes != 400 {
		t.Fatalf("disk write=%v, want 400", o.DiskWriteBytes)
	}
	if o.NetworkRXBytes == nil || *o.NetworkRXBytes != 2000 {
		t.Fatalf("net rx=%v, want 2000", o.NetworkRXBytes)
	}
	if o.NetworkTXBytes == nil || *o.NetworkTXBytes != 300 {
		t.Fatalf("net tx=%v, want 300", o.NetworkTXBytes)
	}
	pageSize := int64(os.Getpagesize())
	if o.SwapInBytes == nil || *o.SwapInBytes != 2*pageSize {
		t.Fatalf("swap in=%v, want %d", o.SwapInBytes, 2*pageSize)
	}
	if o.RSSAvgBytes == nil || *o.RSSAvgBytes != 200*1024 {
		t.Fatalf("rss=%v, want %d", o.RSSAvgBytes, 200*1024)
	}
	if o.RSSPeakBytes == nil || *o.RSSPeakBytes != 180*1024 {
		t.Fatalf("rss peak=%v, want %d", o.RSSPeakBytes, 180*1024)
	}
	if o.CPUTempPeakC == nil || diff(*o.CPUTempPeakC, 52) > 0.01 {
		t.Fatalf("cpu temp=%v, want 52", o.CPUTempPeakC)
	}
	if o.Throttled != nil {
		t.Fatalf("throttled must be nil without throttle counters, got %v", *o.Throttled)
	}
	// No GPU configured → GPU fields stay nil.
	if o.GPUAvgPct != nil || o.DecoderAvgPct != nil {
		t.Fatalf("gpu fields must stay nil without nvidia-smi: %+v", o)
	}
}

func TestCollectHonorsContextCancellation(t *testing.T) {
	root := t.TempDir()
	writeProcFixtures(t, root, 42)
	p := New(Options{ProcRoot: filepath.Join(root, "proc"), SysRoot: filepath.Join(root, "sys"), NvidiaSMI: "none", PID: 42})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := p.Collect(ctx, capperformance.SampleIdentity{}); err == nil {
		t.Fatal("collect must fail on a cancelled context")
	}
}

func mustMkdirAll(t *testing.T, dirs ...string) {
	t.Helper()
	for _, d := range dirs {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func itoa(v int) string {
	if v == 0 {
		return "0"
	}
	neg := v < 0
	if neg {
		v = -v
	}
	var buf [20]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

func diff(a, b float64) float64 {
	if a > b {
		return a - b
	}
	return b - a
}
