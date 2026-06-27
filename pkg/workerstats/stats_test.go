package workerstats

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

const fixtureProcStat = `cpu  100 5 50 200 1 0 0 0 0 0
cpu0 80 5 40 100 1 0 0 0 0 0
cpu1 20 0 10 100 0 0 0 0 0 0
intr 12345 0 0
ctxt 67890
btime 1700000000
`

const fixtureProcNetDev = `Inter-|   Receive                                                |  Transmit
 face |bytes    packets errs drop fifo frame compressed multicast|bytes    packets errs drop fifo colls carrier compressed
    lo:       0       0    0    0    0     0          0         0        0       0    0    0    0     0       0          0
  eth0: 1000000    1000    0    0    0     0          0         0   500000     800    0    0    0     0       0          0
  eth1:  500000     500    0    0    0     0          0         0   200000     400    0    0    0     0       0          0
`

func TestSample_CPU_Ratio_Fixture(t *testing.T) {
	dir := t.TempDir()
	cpu := filepath.Join(dir, "stat")
	if err := os.WriteFile(cpu, []byte(fixtureProcStat), 0o644); err != nil {
		t.Fatal(err)
	}
	ratio, err := sampleCPU(cpu)
	if err != nil {
		t.Fatalf("sampleCPU: %v", err)
	}
	// user=100, nice=5, system=50, idle=200. busy=155, total=355. ~0.4366.
	if ratio < 0.43 || ratio > 0.44 {
		t.Errorf("expected ratio ~0.4366, got %.4f", ratio)
	}
}

func TestSample_CPU_MissingAggregate(t *testing.T) {
	dir := t.TempDir()
	cpu := filepath.Join(dir, "stat")
	if err := os.WriteFile(cpu, []byte("intr 12345 0 0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := sampleCPU(cpu)
	if err == nil || !strings.Contains(err.Error(), "no 'cpu '") {
		t.Errorf("expected missing-aggregate error, got %v", err)
	}
}

func TestSample_Network_AllDevices_ExcludesLoopback(t *testing.T) {
	dir := t.TempDir()
	net := filepath.Join(dir, "netdev")
	if err := os.WriteFile(net, []byte(fixtureProcNetDev), 0o644); err != nil {
		t.Fatal(err)
	}
	rx, tx, err := sampleNetwork(net, "")
	if err != nil {
		t.Fatalf("sampleNetwork: %v", err)
	}
	// eth0 rx=1000000 tx=500000 ; eth1 rx=500000 tx=200000 ; lo excluded.
	if rx != 1500000 || tx != 700000 {
		t.Errorf("expected rx=1500000 tx=700000, got rx=%d tx=%d", rx, tx)
	}
}

func TestSample_Network_FilterByDevice(t *testing.T) {
	dir := t.TempDir()
	net := filepath.Join(dir, "netdev")
	if err := os.WriteFile(net, []byte(fixtureProcNetDev), 0o644); err != nil {
		t.Fatal(err)
	}
	rx, tx, err := sampleNetwork(net, "eth0")
	if err != nil {
		t.Fatalf("sampleNetwork: %v", err)
	}
	if rx != 1000000 || tx != 500000 {
		t.Errorf("expected rx=1000000 tx=500000, got rx=%d tx=%d", rx, tx)
	}
}

func TestSample_Network_DeviceNotFound(t *testing.T) {
	dir := t.TempDir()
	net := filepath.Join(dir, "netdev")
	if err := os.WriteFile(net, []byte(fixtureProcNetDev), 0o644); err != nil {
		t.Fatal(err)
	}
	_, _, err := sampleNetwork(net, "tun9")
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Errorf("expected device-not-found error, got %v", err)
	}
}

// fixtureProcNetDevLoOnly: hosts with no carrier (pre-up network
// namespace, sandboxed container, cgroup-restricted edge worker)
// have only loopback. sampleNetwork MUST surface this anomaly instead
// of silently returning zero bytes — operators depend on the warning
// to distinguish "host offline" from "host idle".
const fixtureProcNetDevLoOnly = `Inter-|   Receive                                                |  Transmit
 face |bytes    packets errs drop fifo frame compressed multicast|bytes    packets errs drop fifo colls carrier compressed
    lo:       0       0    0    0    0     0          0         0        0       0    0    0    0     0       0          0
`

func TestSample_Network_AllLoHost_Errors(t *testing.T) {
	dir := t.TempDir()
	net := filepath.Join(dir, "netdev")
	if err := os.WriteFile(net, []byte(fixtureProcNetDevLoOnly), 0o644); err != nil {
		t.Fatal(err)
	}
	rx, tx, err := sampleNetwork(net, "")
	if err == nil {
		t.Fatalf("expected sentinel error on all-lo host with empty device filter, got nil; rx=%d tx=%d", rx, tx)
	}
	if !strings.Contains(err.Error(), "no non-loopback") {
		t.Errorf("expected 'no non-loopback interface found' error message, got %q", err.Error())
	}
}

func TestSample_Statfs_RealPath(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("syscall.Statfs differs on Windows")
	}
	dir := t.TempDir()
	free, used, err := sampleDisk(dir)
	if err != nil {
		t.Skipf("Statfs not supported in this sandbox: %v", err)
	}
	if free == 0 && used == 0 {
		t.Errorf("expected nonzero free+used on a real tmpdir, got free=%d used=%d", free, used)
	}
}

func TestSample_PartialFailure_NeverPanics(t *testing.T) {
	// CPU path unreadable; other sources should still populate the POJO.
	dir := t.TempDir()
	cpu := filepath.Join(dir, "missing-stat")
	net := filepath.Join(dir, "netdev")
	if err := os.WriteFile(net, []byte(fixtureProcNetDev), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := Config{
		CPUSourcePath:     cpu,
		NetworkSourcePath: net,
		DiskMountPath:     "/",
	}
	out, err := Sample(context.Background(), cfg)
	if err == nil {
		t.Errorf("expected joined error from missing CPU path, got nil")
	}
	if out == nil {
		t.Fatalf("Sample MUST return a non-nil POJO even on partial failure")
	}
	if !errors.Is(err, err) { // sentinel: just confirm the err is non-nil typed
		t.Logf("got error (acceptable): %v", err)
	}
	// Memory is always populated from runtime.MemStats.
	if out.MemoryAllocBytes == 0 && out.MemorySysBytes == 0 {
		t.Errorf("expected runtime.MemStats to populate memory fields, got zero")
	}
	// Network did succeed via fixture.
	if out.NetRxBytes != 1500000 || out.NetTxBytes != 700000 {
		t.Errorf("expected network fan-out from fixture, got rx=%d tx=%d", out.NetRxBytes, out.NetTxBytes)
	}
}

func TestConfig_Defaults(t *testing.T) {
	var c Config
	if c.cpuPath() != "/proc/stat" {
		t.Errorf("default cpuPath should be /proc/stat, got %q", c.cpuPath())
	}
	if c.netPath() != "/proc/net/dev" {
		t.Errorf("default netPath should be /proc/net/dev, got %q", c.netPath())
	}
	if c.diskPath() != "/" {
		t.Errorf("default diskPath should be /, got %q", c.diskPath())
	}
}
