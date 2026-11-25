package renderqueue

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestExecuteMapRendererReportsStartAndExit(t *testing.T) {
	dir := t.TempDir()
	renderer := filepath.Join(dir, "renderer.sh")
	script := `#!/bin/sh
out=""
while [ "$#" -gt 0 ]; do
  case "$1" in
    --output) out="$2"; shift ;;
  esac
  shift
done
sleep 0.05
: > "$out"
`
	if err := os.WriteFile(renderer, []byte(script), 0o755); err != nil {
		t.Fatalf("write renderer: %v", err)
	}
	workspace := mapRenderWorkspace{
		dir:     dir,
		input:   filepath.Join(dir, "map.json"),
		output:  filepath.Join(dir, "map.mp4"),
		summary: filepath.Join(dir, "map.telemetry.json"),
	}
	if err := os.WriteFile(workspace.input, []byte("{}"), 0o600); err != nil {
		t.Fatalf("write input: %v", err)
	}

	events := make(chan MapRenderProgress, 8)
	ctx := WithMapRenderProgress(context.Background(), func(progress MapRenderProgress) {
		events <- progress
	})
	if err := executeMapRenderer(ctx, renderer, workspace); err != nil {
		t.Fatalf("executeMapRenderer: %v", err)
	}

	var got []MapRenderProgress
	for {
		select {
		case progress := <-events:
			got = append(got, progress)
		default:
			if len(got) < 2 {
				t.Fatalf("progress events = %+v, want start and exit", got)
			}
			if got[0].Phase != "start" || got[0].PID <= 0 {
				t.Fatalf("first event = %+v, want start with PID", got[0])
			}
			last := got[len(got)-1]
			if last.Phase != "exit" || last.PID != got[0].PID || last.Elapsed <= 0 || last.Err != nil || last.Result != "ok" || last.ExitCode != 0 {
				t.Fatalf("last event = %+v, want successful exit for pid %d", last, got[0].PID)
			}
			return
		case <-time.After(2 * time.Second):
			t.Fatalf("timed out waiting for map progress events: %+v", got)
		}
	}
}

func TestExecuteMapRendererTimeoutKillsProcessGroup(t *testing.T) {
	dir := t.TempDir()
	renderer := filepath.Join(dir, "renderer.sh")
	childPIDFile := filepath.Join(dir, "child.pid")
	script := "#!/bin/sh\n(sleep 30) &\necho $! > '" + childPIDFile + "'\nwait\n"
	if err := os.WriteFile(renderer, []byte(script), 0o755); err != nil {
		t.Fatalf("write renderer: %v", err)
	}
	workspace := mapRenderWorkspace{dir: dir, input: filepath.Join(dir, "map.json"), output: filepath.Join(dir, "map.mp4"), summary: filepath.Join(dir, "map.telemetry.json")}
	if err := os.WriteFile(workspace.input, []byte("{}"), 0o600); err != nil {
		t.Fatalf("write input: %v", err)
	}
	t.Setenv("PIPELINEGEN_DYNAMIC_MAP_TIMEOUT", "250ms")
	if err := executeMapRenderer(context.Background(), renderer, workspace); err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("executeMapRenderer error = %v, want timeout", err)
	}
	rawPID, err := os.ReadFile(childPIDFile)
	if err != nil {
		t.Fatalf("read child pid: %v", err)
	}
	childPID, err := strconv.Atoi(strings.TrimSpace(string(rawPID)))
	if err != nil || childPID <= 0 {
		t.Fatalf("child pid %q: %v", rawPID, err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		stat, statErr := os.ReadFile(filepath.Join("/proc", strconv.Itoa(childPID), "stat"))
		if os.IsNotExist(statErr) || (statErr == nil && processState(stat) == 'Z') {
			return
		}
		if statErr != nil && !os.IsNotExist(statErr) {
			t.Fatalf("inspect renderer child: %v", statErr)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := syscall.Kill(childPID, 0); err == nil {
		t.Fatalf("renderer child %d survived process-group cancellation", childPID)
	}
}

func processState(stat []byte) byte {
	end := strings.LastIndexByte(string(stat), ')')
	if end < 0 || end+2 >= len(stat) {
		return 0
	}
	return stat[end+2]
}
