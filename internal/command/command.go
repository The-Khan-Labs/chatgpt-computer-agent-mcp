package command

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"chatgpt-computer-agent-mcp/internal/agenterr"
	"chatgpt-computer-agent-mcp/internal/config"
	"chatgpt-computer-agent-mcp/internal/platform"
	"chatgpt-computer-agent-mcp/internal/policy"
)

const (
	maxExecutableBytes = 4096
	maxArguments       = 256
	maxArgumentBytes   = 16 << 10
	maxCommandBytes    = 16 << 10
)

type Directory struct {
	Root string `json:"root"`
	Path string `json:"path"`
}

type Request struct {
	Executable     string    `json:"executable"`
	Arguments      []string  `json:"arguments,omitempty"`
	CWD            Directory `json:"cwd"`
	TimeoutSeconds int       `json:"timeout_seconds,omitempty"`
}

type Result struct {
	Stdout          string  `json:"stdout"`
	Stderr          string  `json:"stderr"`
	ExitCode        *int    `json:"exit_code"`
	Termination     *string `json:"termination"`
	TimedOut        bool    `json:"timed_out"`
	DurationMS      int64   `json:"duration_ms"`
	StdoutTruncated bool    `json:"stdout_truncated"`
	StderrTruncated bool    `json:"stderr_truncated"`
}

type Stream string

const (
	Stdout Stream = "stdout"
	Stderr Stream = "stderr"
)

type Launcher struct {
	policy       *policy.Set
	limits       config.Limits
	startProcess func(platform.Spec) (platform.OwnedProcess, *os.File, *os.File, error)
	mu           sync.Mutex
	closed       bool
	next         uint64
	runs         map[uint64]context.CancelFunc
	wait         sync.WaitGroup
}

type Execution struct {
	process  platform.OwnedProcess
	stdout   *cappedBuffer
	stderr   *cappedBuffer
	started  time.Time
	grace    time.Duration
	done     chan struct{}
	drains   sync.WaitGroup
	stop     sync.Once
	mu       sync.RWMutex
	result   Result
	waitErr  error
	complete bool
	timedOut bool
}

type cappedBuffer struct {
	mu        sync.RWMutex
	data      []byte
	limit     int
	truncated bool
}

func New(approved *policy.Set, limits config.Limits) *Launcher {
	return &Launcher{
		policy: approved, limits: limits, startProcess: platform.Start,
		runs: make(map[uint64]context.CancelFunc),
	}
}

func (l *Launcher) Run(ctx context.Context, request Request) (Result, error) {
	runContext, cancel, id, err := l.beginRun(ctx)
	if err != nil {
		return Result{}, err
	}
	defer l.endRun(id, cancel)
	execution, err := l.start(runContext, request, policy.RunCommand)
	if err != nil {
		return Result{}, err
	}
	timeout := request.TimeoutSeconds
	if timeout == 0 {
		timeout = l.limits.DefaultCommandTimeoutSeconds
	}
	timer := time.NewTimer(time.Duration(timeout) * time.Second)
	defer timer.Stop()
	select {
	case <-execution.Done():
	case <-timer.C:
		execution.markTimedOut()
		_ = execution.Stop(context.Background())
	case <-runContext.Done():
		_ = execution.Stop(context.Background())
	}
	return execution.Wait()
}

func (l *Launcher) Start(ctx context.Context, request Request) (*Execution, error) {
	l.mu.Lock()
	closed := l.closed
	l.mu.Unlock()
	if closed {
		return nil, agenterr.New(agenterr.LaunchFailed, "command launcher is shut down")
	}
	return l.start(ctx, request, policy.ProcessStart)
}

func (l *Launcher) start(ctx context.Context, request Request, capability policy.Capability) (*Execution, error) {
	if err := validateRequest(request, l.limits); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, agenterr.Wrap(agenterr.LaunchFailed, "command launch was cancelled", err)
	}
	authorized, err := l.policy.AuthorizeDirectory(capability, request.CWD.Root, request.CWD.Path)
	if err != nil {
		return nil, err
	}
	process, stdout, stderr, err := l.startProcess(platform.Spec{
		Executable: request.Executable, Arguments: append([]string(nil), request.Arguments...),
		Directory: authorized.NativePath(), DirectoryIdentity: authorized.DirectoryIdentity(),
		Environment: baselineEnvironment(),
	})
	if err != nil {
		return nil, agenterr.Wrap(agenterr.LaunchFailed, fmt.Sprintf("cannot launch executable %q", request.Executable), err)
	}
	execution := &Execution{
		process: process, stdout: &cappedBuffer{limit: l.limits.MaxOutputBytesPerStream},
		stderr: &cappedBuffer{limit: l.limits.MaxOutputBytesPerStream}, started: time.Now(),
		grace: time.Duration(l.limits.ProcessStopGraceSeconds) * time.Second, done: make(chan struct{}),
	}
	execution.drains.Add(2)
	go execution.drain(stdout, execution.stdout)
	go execution.drain(stderr, execution.stderr)
	go execution.reap()
	return execution, nil
}

func (l *Launcher) Shutdown(ctx context.Context) error {
	l.mu.Lock()
	if !l.closed {
		l.closed = true
		for _, cancel := range l.runs {
			cancel()
		}
	}
	l.mu.Unlock()
	done := make(chan struct{})
	go func() {
		l.wait.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (l *Launcher) beginRun(ctx context.Context) (context.Context, context.CancelFunc, uint64, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return nil, nil, 0, agenterr.New(agenterr.LaunchFailed, "command launcher is shut down")
	}
	l.next++
	id := l.next
	runContext, cancel := context.WithCancel(ctx)
	l.runs[id] = cancel
	l.wait.Add(1)
	return runContext, cancel, id, nil
}

func (l *Launcher) endRun(id uint64, cancel context.CancelFunc) {
	cancel()
	l.mu.Lock()
	delete(l.runs, id)
	l.mu.Unlock()
	l.wait.Done()
}

func (e *Execution) StartedAt() time.Time  { return e.started }
func (e *Execution) Done() <-chan struct{} { return e.done }

func (e *Execution) Wait() (Result, error) {
	<-e.done
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.result, e.waitErr
}

func (e *Execution) Output(stream Stream) ([]byte, bool, error) {
	switch stream {
	case Stdout:
		return e.stdout.snapshot()
	case Stderr:
		return e.stderr.snapshot()
	default:
		return nil, false, agenterr.New(agenterr.InvalidInput, "stream must be stdout or stderr")
	}
}

func (e *Execution) Stop(ctx context.Context) error {
	e.stop.Do(func() {
		e.process.RequestGracefulStop()
		go func() {
			_ = e.process.GracefulStop()
			timer := time.NewTimer(e.grace)
			defer timer.Stop()
			select {
			case <-e.done:
				return
			case <-timer.C:
				_ = e.process.HardStop()
			}
		}()
	})
	select {
	case <-e.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (e *Execution) markTimedOut() {
	e.mu.Lock()
	e.timedOut = true
	e.mu.Unlock()
}

func (e *Execution) drain(reader *os.File, destination *cappedBuffer) {
	defer e.drains.Done()
	defer func() { _ = reader.Close() }()
	_, _ = io.Copy(destination, reader)
}

func (e *Execution) reap() {
	exit, err := e.process.Wait()
	e.drains.Wait()
	closeErr := e.process.Close()
	stdout, stdoutTruncated, _ := e.stdout.snapshot()
	stderr, stderrTruncated, _ := e.stderr.snapshot()
	e.mu.Lock()
	e.result = Result{
		Stdout: strings.ToValidUTF8(string(stdout), "\uFFFD"), Stderr: strings.ToValidUTF8(string(stderr), "\uFFFD"),
		ExitCode: exit.ExitCode, Termination: exit.Termination, TimedOut: e.timedOut,
		DurationMS:      time.Since(e.started).Milliseconds(),
		StdoutTruncated: stdoutTruncated, StderrTruncated: stderrTruncated,
	}
	e.waitErr = errors.Join(err, closeErr)
	e.complete = true
	e.mu.Unlock()
	close(e.done)
}

func (b *cappedBuffer) Write(data []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	remaining := b.limit - len(b.data)
	if remaining > 0 {
		b.data = append(b.data, data[:min(remaining, len(data))]...)
	}
	if len(data) > remaining {
		b.truncated = true
	}
	return len(data), nil
}

func (b *cappedBuffer) snapshot() ([]byte, bool, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return bytes.Clone(b.data), b.truncated, nil
}

func validateRequest(request Request, limits config.Limits) error {
	if len(request.Executable) == 0 || len(request.Executable) > maxExecutableBytes || strings.IndexByte(request.Executable, 0) >= 0 || !utf8.ValidString(request.Executable) {
		return agenterr.New(agenterr.InvalidInput, "executable must be 1 to 4096 bytes of NUL-free UTF-8")
	}
	if len(request.Arguments) > maxArguments {
		return agenterr.New(agenterr.InvalidInput, "arguments may contain at most 256 items")
	}
	total := len(request.Executable)
	for _, argument := range request.Arguments {
		if len(argument) > maxArgumentBytes || strings.IndexByte(argument, 0) >= 0 || !utf8.ValidString(argument) {
			return agenterr.New(agenterr.InvalidInput, "each argument must be at most 16384 bytes of NUL-free UTF-8")
		}
		total += len(argument)
	}
	if total > maxCommandBytes {
		return agenterr.New(agenterr.InvalidInput, "executable and arguments exceed 16384 bytes")
	}
	if request.TimeoutSeconds < 0 || request.TimeoutSeconds > limits.MaxCommandTimeoutSeconds {
		return agenterr.New(agenterr.InvalidInput, fmt.Sprintf("timeout_seconds must be between 1 and %d when provided", limits.MaxCommandTimeoutSeconds))
	}
	return nil
}
