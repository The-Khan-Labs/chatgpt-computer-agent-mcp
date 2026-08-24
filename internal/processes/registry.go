package processes

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"chatgpt-computer-agent-mcp/internal/agenterr"
	"chatgpt-computer-agent-mcp/internal/command"
)

const (
	defaultOutputBytes = 65536
	maxOutputBytes     = 65536
)

type State string

const (
	Running State = "running"
	Exited  State = "exited"
	Stopped State = "stopped"
)

type StartRequest struct {
	Executable string            `json:"executable"`
	Arguments  []string          `json:"arguments,omitempty"`
	CWD        command.Directory `json:"cwd"`
}

type StartResult struct {
	ProcessID   string  `json:"process_id"`
	State       State   `json:"state"`
	StartedAt   string  `json:"started_at"`
	FinishedAt  *string `json:"finished_at"`
	ExitCode    *int    `json:"exit_code"`
	Termination *string `json:"termination"`
}

type StatusResult struct {
	ProcessID       string  `json:"process_id"`
	State           State   `json:"state"`
	ExitCode        *int    `json:"exit_code"`
	Termination     *string `json:"termination"`
	StartedAt       string  `json:"started_at"`
	FinishedAt      *string `json:"finished_at"`
	DurationMS      int64   `json:"duration_ms"`
	StdoutBytes     int     `json:"stdout_bytes"`
	StderrBytes     int     `json:"stderr_bytes"`
	StdoutTruncated bool    `json:"stdout_truncated"`
	StderrTruncated bool    `json:"stderr_truncated"`
}

type OutputRequest struct {
	ProcessID string         `json:"process_id"`
	Stream    command.Stream `json:"stream"`
	Offset    int            `json:"offset,omitempty"`
	MaxBytes  int            `json:"max_bytes,omitempty"`
}

type OutputResult struct {
	ProcessID   string         `json:"process_id"`
	Stream      command.Stream `json:"stream"`
	Data        string         `json:"data"`
	Offset      int            `json:"offset"`
	NextOffset  int            `json:"next_offset"`
	EndOfStream bool           `json:"end_of_stream"`
	Truncated   bool           `json:"truncated"`
}

type Registry struct {
	mu       sync.Mutex
	launcher *command.Launcher
	maximum  int
	records  map[string]*record
	closed   bool
}

type record struct {
	id            string
	execution     *command.Execution
	started       time.Time
	finished      time.Time
	result        command.Result
	waitErr       error
	complete      bool
	stopRequested bool
}

func New(launcher *command.Launcher, maximum int) *Registry {
	return &Registry{launcher: launcher, maximum: maximum, records: make(map[string]*record)}
}

func (r *Registry) Start(ctx context.Context, request StartRequest) (StartResult, error) {
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return StartResult{}, agenterr.New(agenterr.LaunchFailed, "process registry is shut down")
	}
	candidate := r.evictionCandidateLocked()
	if len(r.records) >= r.maximum && candidate == nil {
		r.mu.Unlock()
		return StartResult{}, agenterr.New(agenterr.ProcessLimit, fmt.Sprintf("all %d process slots are running", r.maximum))
	}
	id, err := r.newIDLocked()
	if err != nil {
		r.mu.Unlock()
		return StartResult{}, err
	}
	execution, err := r.launcher.Start(ctx, command.Request{
		Executable: request.Executable, Arguments: append([]string(nil), request.Arguments...), CWD: request.CWD,
	})
	if err != nil {
		r.mu.Unlock()
		return StartResult{}, err
	}
	if candidate != nil {
		delete(r.records, candidate.id)
	}
	entry := &record{id: id, execution: execution, started: execution.StartedAt()}
	r.records[id] = entry
	r.refreshLocked(entry)
	result := r.startResultLocked(entry)
	r.mu.Unlock()
	go r.observe(entry)
	return result, nil
}

func (r *Registry) Status(processID string) (StatusResult, error) {
	if err := validateProcessID(processID); err != nil {
		return StatusResult{}, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	entry, err := r.recordLocked(processID)
	if err != nil {
		return StatusResult{}, err
	}
	return r.statusLocked(entry)
}

func (r *Registry) Output(request OutputRequest) (OutputResult, error) {
	if err := validateProcessID(request.ProcessID); err != nil {
		return OutputResult{}, err
	}
	if request.Stream != command.Stdout && request.Stream != command.Stderr {
		return OutputResult{}, agenterr.New(agenterr.InvalidInput, "stream must be stdout or stderr")
	}
	if request.Offset < 0 {
		return OutputResult{}, agenterr.New(agenterr.InvalidInput, "offset must not be negative")
	}
	limit := request.MaxBytes
	if limit == 0 {
		limit = defaultOutputBytes
	}
	if limit < 1 || limit > maxOutputBytes {
		return OutputResult{}, agenterr.New(agenterr.InvalidInput, "max_bytes must be between 1 and 65536")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	entry, err := r.recordLocked(request.ProcessID)
	if err != nil {
		return OutputResult{}, err
	}
	data, truncated, err := entry.execution.Output(request.Stream)
	if err != nil {
		return OutputResult{}, err
	}
	start := min(request.Offset, len(data))
	end := min(start+limit, len(data))
	next := request.Offset + end - start
	return OutputResult{
		ProcessID: request.ProcessID, Stream: request.Stream,
		Data:   strings.ToValidUTF8(string(data[start:end]), "\uFFFD"),
		Offset: request.Offset, NextOffset: next,
		EndOfStream: entry.complete && end == len(data), Truncated: truncated,
	}, nil
}

func (r *Registry) Stop(ctx context.Context, processID string) (StatusResult, error) {
	if err := validateProcessID(processID); err != nil {
		return StatusResult{}, err
	}
	r.mu.Lock()
	entry, err := r.recordLocked(processID)
	if err != nil {
		r.mu.Unlock()
		return StatusResult{}, err
	}
	if entry.complete {
		result, statusErr := r.statusLocked(entry)
		r.mu.Unlock()
		return result, statusErr
	}
	entry.stopRequested = true
	execution := entry.execution
	r.mu.Unlock()
	if err := execution.Stop(ctx); err != nil {
		return StatusResult{}, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.refreshLocked(entry)
	return r.statusLocked(entry)
}

func (r *Registry) Count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.records)
}

func (r *Registry) Shutdown(ctx context.Context) error {
	r.mu.Lock()
	r.closed = true
	executions := make([]*command.Execution, 0, len(r.records))
	for _, entry := range r.records {
		r.refreshLocked(entry)
		if !entry.complete {
			entry.stopRequested = true
			executions = append(executions, entry.execution)
		}
	}
	r.mu.Unlock()

	var wait sync.WaitGroup
	errorsByProcess := make(chan error, len(executions))
	for _, execution := range executions {
		wait.Add(1)
		go func() {
			defer wait.Done()
			errorsByProcess <- execution.Stop(ctx)
		}()
	}
	wait.Wait()
	close(errorsByProcess)
	var stopErrors []error
	for err := range errorsByProcess {
		stopErrors = append(stopErrors, err)
	}
	return errors.Join(stopErrors...)
}

func (r *Registry) observe(entry *record) {
	result, err := entry.execution.Wait()
	r.mu.Lock()
	defer r.mu.Unlock()
	r.finishLocked(entry, result, err)
}

func (r *Registry) refreshLocked(entry *record) {
	if entry.complete {
		return
	}
	select {
	case <-entry.execution.Done():
		result, err := entry.execution.Wait()
		r.finishLocked(entry, result, err)
	default:
	}
}

func (r *Registry) finishLocked(entry *record, result command.Result, err error) {
	if entry.complete {
		return
	}
	entry.result = result
	entry.waitErr = err
	entry.complete = true
	entry.finished = entry.started.Add(time.Duration(result.DurationMS) * time.Millisecond)
}

func (r *Registry) evictionCandidateLocked() *record {
	if len(r.records) < r.maximum {
		return nil
	}
	var oldest *record
	for _, entry := range r.records {
		r.refreshLocked(entry)
		if !entry.complete {
			continue
		}
		if oldest == nil || entry.finished.Before(oldest.finished) {
			oldest = entry
		}
	}
	return oldest
}

func (r *Registry) recordLocked(processID string) (*record, error) {
	entry := r.records[processID]
	if entry == nil {
		return nil, agenterr.New(agenterr.ProcessNotFound, "managed process not found")
	}
	r.refreshLocked(entry)
	return entry, nil
}

func (r *Registry) startResultLocked(entry *record) StartResult {
	state, finished, exitCode, termination := r.outcomeLocked(entry)
	return StartResult{
		ProcessID: entry.id, State: state, StartedAt: timestamp(entry.started), FinishedAt: finished,
		ExitCode: exitCode, Termination: termination,
	}
}

func (r *Registry) statusLocked(entry *record) (StatusResult, error) {
	if entry.waitErr != nil {
		return StatusResult{}, agenterr.Wrap(agenterr.InternalError, "cannot collect managed process", entry.waitErr)
	}
	stdout, stdoutTruncated, err := entry.execution.Output(command.Stdout)
	if err != nil {
		return StatusResult{}, err
	}
	stderr, stderrTruncated, err := entry.execution.Output(command.Stderr)
	if err != nil {
		return StatusResult{}, err
	}
	state, finished, exitCode, termination := r.outcomeLocked(entry)
	duration := time.Since(entry.started).Milliseconds()
	if entry.complete {
		duration = entry.result.DurationMS
	}
	return StatusResult{
		ProcessID: entry.id, State: state, ExitCode: exitCode, Termination: termination,
		StartedAt: timestamp(entry.started), FinishedAt: finished, DurationMS: duration,
		StdoutBytes: len(stdout), StderrBytes: len(stderr),
		StdoutTruncated: stdoutTruncated, StderrTruncated: stderrTruncated,
	}, nil
}

func (r *Registry) outcomeLocked(entry *record) (State, *string, *int, *string) {
	if !entry.complete {
		return Running, nil, nil, nil
	}
	state := Exited
	termination := entry.result.Termination
	if entry.stopRequested {
		state = Stopped
		if termination == nil {
			value := "stopped"
			termination = &value
		}
	}
	finished := timestamp(entry.finished)
	return state, &finished, entry.result.ExitCode, termination
}

func (r *Registry) newIDLocked() (string, error) {
	for range 10 {
		var value [16]byte
		if _, err := rand.Read(value[:]); err != nil {
			return "", agenterr.Wrap(agenterr.InternalError, "cannot allocate a process ID", err)
		}
		id := hex.EncodeToString(value[:])
		if r.records[id] == nil {
			return id, nil
		}
	}
	return "", agenterr.New(agenterr.InternalError, "cannot allocate a unique process ID")
}

func validateProcessID(processID string) error {
	if len(processID) < 1 || len(processID) > 128 || strings.IndexByte(processID, 0) >= 0 {
		return agenterr.New(agenterr.InvalidInput, "process_id must be 1 to 128 bytes and contain no NUL")
	}
	return nil
}

func timestamp(value time.Time) string { return value.UTC().Format(time.RFC3339Nano) }
