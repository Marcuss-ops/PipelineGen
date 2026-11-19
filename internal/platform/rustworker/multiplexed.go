package rustworker

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"syscall"
)

// MultiplexedPersistentRunner keeps one ordered NDJSON worker process and
// allows several requests to be in flight. It relies on the worker contract
// that every nonblank input line produces exactly one response line in input
// order (the BrainRust semantic-engine dispatcher provides this guarantee).
type MultiplexedPersistentRunner struct {
	mu          sync.Mutex
	state       *multiplexedProcess
	maxInFlight int
	// env carries explicit KEY=VALUE overrides for the worker process. Nil
	// keeps the historical inherited environment.
	env []string
}

type multiplexedProcess struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout *bufio.Reader
	stderr *BoundedBuffer

	mu      sync.Mutex
	err     error
	pending []*multiplexedCall

	writeMu    sync.Mutex
	permits    chan struct{}
	kill       sync.Once
	reap       sync.Once
	readerDone chan struct{}
	pump       sync.WaitGroup
}

type multiplexedCall struct {
	result    chan multiplexedResult
	limit     int64
	once      sync.Once
	owner     *multiplexedProcess
	mu        sync.Mutex
	delivered bool
	abandoned bool
}

type multiplexedResult struct {
	stdout []byte
	stderr []byte
	err    error
}

func NewMultiplexedPersistentRunner(maxInFlight int) *MultiplexedPersistentRunner {
	return NewMultiplexedPersistentRunnerWithEnv(maxInFlight, nil)
}

// NewMultiplexedPersistentRunnerWithEnv starts the worker with explicit
// KEY=VALUE overrides on top of the inherited environment. It is how a caller
// opts a worker into a feature (for example SEMANTIC_CACHE_DIR) without
// mutating the whole process environment.
func NewMultiplexedPersistentRunnerWithEnv(maxInFlight int, env []string) *MultiplexedPersistentRunner {
	if maxInFlight < 1 {
		maxInFlight = 1
	}
	return &MultiplexedPersistentRunner{
		maxInFlight: maxInFlight,
		env:         append([]string(nil), env...),
	}
}

func (r *MultiplexedPersistentRunner) Run(ctx context.Context, binary string, input []byte, outputLimit int64) ([]byte, []byte, error) {
	input = bytes.TrimSpace(input)
	if len(input) == 0 {
		return nil, nil, fmt.Errorf("multiplexed Rust request is empty")
	}
	if bytes.IndexAny(input, "\r\n") >= 0 {
		return nil, nil, fmt.Errorf("multiplexed Rust request must contain exactly one NDJSON line")
	}
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	state, err := r.ensure(binary, outputLimit)
	if err != nil {
		return nil, nil, err
	}
	select {
	case state.permits <- struct{}{}:
	case <-ctx.Done():
		return nil, state.stderr.Bytes(), ctx.Err()
	}
	call := &multiplexedCall{result: make(chan multiplexedResult, 1), limit: outputLimit, owner: state}

	input = append(append([]byte(nil), input...), '\n')
	state.writeMu.Lock()
	state.mu.Lock()
	if state.err != nil {
		err = state.err
		state.mu.Unlock()
		state.writeMu.Unlock()
		call.release()
		return nil, state.stderr.Bytes(), err
	}
	state.pending = append(state.pending, call)
	state.mu.Unlock()
	if err = writeAll(state.stdin, input); err != nil {
		state.writeMu.Unlock()
		r.failState(state, fmt.Errorf("write multiplexed Rust request: %w", err), false)
	} else {
		state.writeMu.Unlock()
	}

	select {
	case result := <-call.result:
		call.release()
		return result.stdout, result.stderr, result.err
	case <-ctx.Done():
		// Keep the call in the response FIFO until its line is consumed. Its
		// slot remains held until that reply arrives, so cancelled calls cannot
		// desynchronize later replies or create an unbounded orphaned queue.
		call.abandon()
		return nil, state.stderr.Bytes(), ctx.Err()
	}
}

func (r *MultiplexedPersistentRunner) ensure(binary string, outputLimit int64) (*multiplexedProcess, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.state != nil {
		r.state.mu.Lock()
		alive := r.state.err == nil
		r.state.mu.Unlock()
		if alive {
			return r.state, nil
		}
		r.state = nil
	}
	cmd := exec.Command(binary)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if len(r.env) > 0 {
		cmd.Env = append(os.Environ(), r.env...)
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("open multiplexed Rust stdin: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return nil, fmt.Errorf("open multiplexed Rust stdout: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		_ = stdin.Close()
		return nil, fmt.Errorf("open multiplexed Rust stderr: %w", err)
	}
	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		return nil, fmt.Errorf("start multiplexed Rust worker: %w", err)
	}
	state := &multiplexedProcess{
		cmd: cmd, stdin: stdin, stdout: bufio.NewReader(stdout),
		stderr: &BoundedBuffer{Limit: outputLimit}, permits: make(chan struct{}, r.maxInFlight),
		readerDone: make(chan struct{}),
	}
	r.state = state
	state.pump.Add(1)
	go func(tail *BoundedBuffer) {
		defer state.pump.Done()
		if _, copyErr := io.Copy(tail, stderr); copyErr != nil {
			_, _ = tail.Write([]byte("[rustworker stderr copy failed]"))
		}
	}(state.stderr)
	go r.readLoop(state)
	return state, nil
}

func (r *MultiplexedPersistentRunner) readLoop(state *multiplexedProcess) {
	defer func() {
		close(state.readerDone)
		state.reapProcess()
	}()
	for {
		line, err := state.stdout.ReadBytes('\n')
		if err != nil {
			r.failState(state, fmt.Errorf("read multiplexed Rust response: %w", err), true)
			return
		}
		state.mu.Lock()
		if len(state.pending) == 0 {
			state.mu.Unlock()
			r.failState(state, errors.New("multiplexed Rust worker emitted an unsolicited response"), true)
			return
		}
		call := state.pending[0]
		state.pending[0] = nil
		state.pending = state.pending[1:]
		state.mu.Unlock()

		result := multiplexedResult{stdout: line, stderr: state.stderr.Bytes()}
		if call.limit > 0 && int64(len(line)) > call.limit {
			result.stdout = nil
			result.err = errors.New("multiplexed Rust response exceeds output limit")
		}
		call.deliver(result)
	}
}

func (r *MultiplexedPersistentRunner) failState(state *multiplexedProcess, cause error, fromReader bool) {
	state.mu.Lock()
	if state.err != nil {
		state.mu.Unlock()
		if !fromReader {
			state.stopProcess()
		}
		return
	}
	state.err = cause
	pending := state.pending
	state.pending = nil
	state.mu.Unlock()

	for _, call := range pending {
		call.deliver(multiplexedResult{stderr: state.stderr.Bytes(), err: cause})
	}
	r.mu.Lock()
	if r.state == state {
		r.state = nil
	}
	r.mu.Unlock()
	if fromReader {
		state.killProcess()
		return
	}
	state.stopProcess()
}

func (state *multiplexedProcess) killProcess() {
	state.kill.Do(func() {
		_ = state.stdin.Close()
		if state.cmd.Process != nil {
			_ = syscall.Kill(-state.cmd.Process.Pid, syscall.SIGKILL)
		}
	})
}

func (state *multiplexedProcess) stopProcess() {
	state.killProcess()
	<-state.readerDone
	state.reapProcess()
}

func (state *multiplexedProcess) reapProcess() {
	state.reap.Do(func() {
		_ = state.cmd.Wait()
		state.pump.Wait()
	})
}

func (c *multiplexedCall) release() {
	c.once.Do(func() { <-c.owner.permits })
}

func (c *multiplexedCall) deliver(result multiplexedResult) {
	c.mu.Lock()
	c.delivered = true
	abandoned := c.abandoned
	c.mu.Unlock()
	c.result <- result
	if abandoned {
		c.release()
	}
}

func (c *multiplexedCall) abandon() {
	c.mu.Lock()
	c.abandoned = true
	delivered := c.delivered
	c.mu.Unlock()
	if delivered {
		c.release()
	}
}

func writeAll(writer io.Writer, data []byte) error {
	for len(data) > 0 {
		written, err := writer.Write(data)
		if err != nil {
			return err
		}
		if written == 0 {
			return io.ErrShortWrite
		}
		data = data[written:]
	}
	return nil
}

// Reset stops the worker and fails every request currently waiting for a
// response. A subsequent Run starts a fresh process.
func (r *MultiplexedPersistentRunner) Reset() {
	r.mu.Lock()
	state := r.state
	r.state = nil
	r.mu.Unlock()
	if state != nil {
		r.failState(state, errors.New("multiplexed Rust worker reset"), false)
	}
}
