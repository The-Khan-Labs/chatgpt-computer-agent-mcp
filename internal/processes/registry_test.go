package processes

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"chatgpt-computer-agent-mcp/internal/agenterr"
	"chatgpt-computer-agent-mcp/internal/command"
	"chatgpt-computer-agent-mcp/internal/config"
	"chatgpt-computer-agent-mcp/internal/policy"
)

func TestRegistryLifecycleStatusAndRawOutputPaging(t *testing.T) {
	registry, executable := newRegistry(t, 3, 64)
	started, err := registry.Start(context.Background(), helperStart(executable, "emit", "250ms"))
	if err != nil {
		t.Fatal(err)
	}
	if len(started.ProcessID) != 32 {
		t.Fatalf("process id=%q", started.ProcessID)
	}
	if _, err := hex.DecodeString(started.ProcessID); err != nil {
		t.Fatalf("process ID is not hex: %v", err)
	}
	if started.State != Running || started.FinishedAt != nil {
		t.Fatalf("unexpected start result: %+v", started)
	}

	first := waitForOutput(t, registry, OutputRequest{ProcessID: started.ProcessID, Stream: command.Stdout, MaxBytes: 2})
	if first.Data != "a\uFFFD" || first.Offset != 0 || first.NextOffset != 2 || first.EndOfStream {
		t.Fatalf("first page: %+v", first)
	}
	second, err := registry.Output(OutputRequest{ProcessID: started.ProcessID, Stream: command.Stdout, Offset: 2, MaxBytes: 2})
	if err != nil {
		t.Fatal(err)
	}
	if second.Data != "bc" || second.NextOffset != 4 {
		t.Fatalf("second page: %+v", second)
	}
	stderr := waitForOutput(t, registry, OutputRequest{ProcessID: started.ProcessID, Stream: command.Stderr})
	if stderr.Data != "ERR" {
		t.Fatalf("stderr page: %+v", stderr)
	}

	status := waitForState(t, registry, started.ProcessID, Exited)
	if status.ExitCode == nil || *status.ExitCode != 0 || status.FinishedAt == nil || status.DurationMS < 200 {
		t.Fatalf("final status: %+v", status)
	}
	end, err := registry.Output(OutputRequest{ProcessID: started.ProcessID, Stream: command.Stdout, Offset: 4})
	if err != nil {
		t.Fatal(err)
	}
	if !end.EndOfStream || end.NextOffset != 4 || end.Data != "" {
		t.Fatalf("end page: %+v", end)
	}
}

func TestRegistryRetainsTruncatedOutputWhileDraining(t *testing.T) {
	registry, executable := newRegistry(t, 1, 4)
	started, err := registry.Start(context.Background(), helperStart(executable, "large"))
	if err != nil {
		t.Fatal(err)
	}
	status := waitForState(t, registry, started.ProcessID, Exited)
	if status.StdoutBytes != 4 || status.StderrBytes != 4 || !status.StdoutTruncated || !status.StderrTruncated {
		t.Fatalf("status: %+v", status)
	}
	output, err := registry.Output(OutputRequest{ProcessID: started.ProcessID, Stream: command.Stdout})
	if err != nil {
		t.Fatal(err)
	}
	if output.Data != "oooo" || !output.Truncated || !output.EndOfStream {
		t.Fatalf("output: %+v", output)
	}
}

func TestRegistryCapacityEvictsOldestCompletedOnly(t *testing.T) {
	registry, executable := newRegistry(t, 2, 64)
	first, err := registry.Start(context.Background(), helperStart(executable, "sleep", "30s"))
	if err != nil {
		t.Fatal(err)
	}
	second, err := registry.Start(context.Background(), helperStart(executable, "sleep", "30s"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = registry.Start(context.Background(), helperStart(executable, "sleep", "30s"))
	if agenterr.CodeOf(err) != agenterr.ProcessLimit {
		t.Fatalf("full running registry: %v", err)
	}
	if _, err := registry.Stop(context.Background(), first.ProcessID); err != nil {
		t.Fatal(err)
	}
	third, err := registry.Start(context.Background(), helperStart(executable, "sleep", "30s"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Status(first.ProcessID); agenterr.CodeOf(err) != agenterr.ProcessNotFound {
		t.Fatalf("oldest completion was not evicted: %v", err)
	}
	if _, err := registry.Status(second.ProcessID); err != nil {
		t.Fatalf("running record was evicted: %v", err)
	}
	if _, err := registry.Status(third.ProcessID); err != nil {
		t.Fatal(err)
	}
}

func TestRegistryLaunchFailureDoesNotEvictCompletedRecord(t *testing.T) {
	registry, executable := newRegistry(t, 1, 64)
	started, err := registry.Start(context.Background(), helperStart(executable, "emit", "0s"))
	if err != nil {
		t.Fatal(err)
	}
	waitForState(t, registry, started.ProcessID, Exited)
	_, err = registry.Start(context.Background(), StartRequest{
		Executable: filepath.Join(t.TempDir(), "missing"), CWD: command.Directory{Root: "workspace", Path: "."},
	})
	if agenterr.CodeOf(err) != agenterr.LaunchFailed {
		t.Fatalf("launch failure: %v", err)
	}
	if _, err := registry.Status(started.ProcessID); err != nil {
		t.Fatalf("completed record was evicted after failed launch: %v", err)
	}
}

func TestRegistryConcurrentStopIsIdempotent(t *testing.T) {
	registry, executable := newRegistry(t, 1, 64)
	started, err := registry.Start(context.Background(), helperStart(executable, "sleep", "30s"))
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			status, stopErr := registry.Stop(context.Background(), started.ProcessID)
			if stopErr != nil {
				t.Errorf("stop: %v", stopErr)
				return
			}
			if status.State != Stopped {
				t.Errorf("state=%s", status.State)
			}
		}()
	}
	wg.Wait()
	status, err := registry.Stop(context.Background(), started.ProcessID)
	if err != nil || status.State != Stopped {
		t.Fatalf("repeated stop: status=%+v err=%v", status, err)
	}
}

func TestRegistryShutdownStopsAllProcessesConcurrently(t *testing.T) {
	registry, executable := newRegistry(t, 3, 64)
	ids := make([]string, 0, 3)
	for range 3 {
		started, err := registry.Start(context.Background(), helperStart(executable, "sleep", "30s"))
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, started.ProcessID)
	}
	startedAt := time.Now()
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := registry.Shutdown(context.Background()); err != nil {
				t.Errorf("shutdown: %v", err)
			}
		}()
	}
	wg.Wait()
	if elapsed := time.Since(startedAt); elapsed > 4*time.Second {
		t.Fatalf("shutdown serialized grace periods: %v", elapsed)
	}
	for _, id := range ids {
		status, err := registry.Status(id)
		if err != nil || status.State != Stopped {
			t.Fatalf("status %s: %+v %v", id, status, err)
		}
	}
	_, err := registry.Start(context.Background(), helperStart(executable, "emit", "0s"))
	if agenterr.CodeOf(err) != agenterr.LaunchFailed {
		t.Fatalf("start after shutdown: %v", err)
	}
}

func TestRegistryValidatesIdentifiersAndOutputRequest(t *testing.T) {
	registry, _ := newRegistry(t, 1, 64)
	for _, id := range []string{"", strings.Repeat("x", 129), "bad\x00id"} {
		if _, err := registry.Status(id); agenterr.CodeOf(err) != agenterr.InvalidInput {
			t.Errorf("id %q: %v", id, err)
		}
	}
	_, err := registry.Output(OutputRequest{ProcessID: "missing", Stream: "bad"})
	if agenterr.CodeOf(err) != agenterr.InvalidInput {
		t.Fatalf("stream: %v", err)
	}
	_, err = registry.Output(OutputRequest{ProcessID: "missing", Stream: command.Stdout, Offset: -1})
	if agenterr.CodeOf(err) != agenterr.InvalidInput {
		t.Fatalf("offset: %v", err)
	}
	_, err = registry.Output(OutputRequest{ProcessID: "missing", Stream: command.Stdout, MaxBytes: 65537})
	if agenterr.CodeOf(err) != agenterr.InvalidInput {
		t.Fatalf("max bytes: %v", err)
	}
}

func TestRegistryHelper(t *testing.T) {
	separator := -1
	for i, argument := range os.Args {
		if argument == "--" {
			separator = i
			break
		}
	}
	if separator < 0 || separator+1 >= len(os.Args) {
		return
	}
	arguments := os.Args[separator+1:]
	switch arguments[0] {
	case "emit":
		_, _ = os.Stdout.Write([]byte{'a', 0xff, 'b', 'c'})
		_, _ = fmt.Fprint(os.Stderr, "ERR")
		duration, _ := time.ParseDuration(arguments[1])
		time.Sleep(duration)
	case "large":
		_, _ = fmt.Fprint(os.Stdout, strings.Repeat("o", 100_000))
		_, _ = fmt.Fprint(os.Stderr, strings.Repeat("e", 100_000))
	case "sleep":
		duration, _ := time.ParseDuration(arguments[1])
		time.Sleep(duration)
	}
	os.Exit(0)
}

func helperStart(executable, mode string, arguments ...string) StartRequest {
	return StartRequest{
		Executable: executable,
		Arguments:  append([]string{"-test.run=TestRegistryHelper", "--", mode}, arguments...),
		CWD:        command.Directory{Root: "workspace", Path: "."},
	}
}

func newRegistry(t *testing.T, maximum, outputLimit int) (*Registry, string) {
	t.Helper()
	root := t.TempDir()
	configPath := filepath.Join(t.TempDir(), "config.json")
	document := map[string]any{
		"version": 1,
		"mode":    "user-shell",
		"roots":   []map[string]string{{"name": "workspace", "path": root}},
		"limits": map[string]int{
			"max_background_processes":        maximum,
			"max_output_bytes_per_stream":     outputLimit,
			"default_command_timeout_seconds": 30,
			"process_stop_grace_seconds":      1,
		},
	}
	data, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, data, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	roots, err := policy.Open(cfg)
	if err != nil {
		t.Fatal(err)
	}
	launcher := command.New(roots, cfg.Limits())
	registry := New(launcher, cfg.Limits().MaxBackgroundProcesses)
	t.Cleanup(func() {
		_ = registry.Shutdown(context.Background())
		_ = launcher.Shutdown(context.Background())
		_ = roots.Close()
	})
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return registry, executable
}

func waitForState(t *testing.T, registry *Registry, id string, want State) StatusResult {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		status, err := registry.Status(id)
		if err != nil {
			t.Fatal(err)
		}
		if status.State == want {
			return status
		}
		if time.Now().After(deadline) {
			t.Fatalf("state=%s want=%s", status.State, want)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func waitForOutput(t *testing.T, registry *Registry, request OutputRequest) OutputResult {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		result, err := registry.Output(request)
		if err != nil {
			t.Fatal(err)
		}
		if result.Data != "" {
			return result
		}
		if time.Now().After(deadline) {
			t.Fatal("no process output")
		}
		time.Sleep(10 * time.Millisecond)
	}
}
