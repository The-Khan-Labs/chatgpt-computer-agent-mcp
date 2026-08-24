package command

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"chatgpt-computer-agent-mcp/internal/agenterr"
	"chatgpt-computer-agent-mcp/internal/config"
	"chatgpt-computer-agent-mcp/internal/platform"
	"chatgpt-computer-agent-mcp/internal/policy"
)

func TestRunPreservesArgumentsDirectoryEnvironmentAndStreams(t *testing.T) {
	root := t.TempDir()
	configuredRoot := root
	if runtime.GOOS != "windows" {
		configuredRoot = filepath.Join(t.TempDir(), "root-link")
		if err := os.Symlink(root, configuredRoot); err != nil {
			t.Fatal(err)
		}
	}
	launcher, root := newLauncherModeAt(t, config.UserShell, 4096, configuredRoot)
	t.Setenv("COMPUTER_AGENT_TEST_SECRET", "must-not-leak")
	t.Setenv("OPENAI_API_KEY", "must-not-leak")
	t.Setenv("LANG", "C.UTF-8")
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	request := helperRequest(executable, "inspect", "two words", "雪☃", `quote"slash\`)
	request.CWD = Directory{Root: "workspace", Path: "."}
	result, err := launcher.Run(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if result.ExitCode == nil || *result.ExitCode != 0 || result.Termination != nil || result.TimedOut {
		t.Fatalf("unexpected exit: %+v", result)
	}
	var output helperInspection
	if err := json.Unmarshal([]byte(result.Stdout), &output); err != nil {
		t.Fatalf("stdout=%q: %v", result.Stdout, err)
	}
	if strings.Join(output.Args, "|") != `two words|雪☃|quote"slash\` {
		t.Fatalf("arguments changed: %#v", output.Args)
	}
	launchedDirectory, err := os.Stat(output.Directory)
	if err != nil {
		t.Fatal(err)
	}
	approvedDirectory, err := os.Stat(root)
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(launchedDirectory, approvedDirectory) {
		t.Fatalf("cwd=%q is not approved directory %q", output.Directory, root)
	}
	if runtime.GOOS == "windows" {
		if _, ok := output.Environment["LANG"]; ok {
			t.Fatalf("LANG was forwarded on Windows: %#v", output.Environment)
		}
		if value, ok := os.LookupEnv("SystemRoot"); ok && output.Environment["SystemRoot"] != value {
			t.Fatalf("SystemRoot not forwarded: %#v", output.Environment)
		}
	} else if output.Environment["LANG"] != "C.UTF-8" {
		t.Fatalf("LANG not forwarded: %#v", output.Environment)
	}
	for _, name := range []string{"COMPUTER_AGENT_TEST_SECRET", "OPENAI_API_KEY"} {
		if _, ok := output.Environment[name]; ok {
			t.Fatalf("secret variable %s was forwarded", name)
		}
	}
	if result.Stderr != "stderr: 雪☃" {
		t.Fatalf("stderr=%q", result.Stderr)
	}
}

func TestRunReportsNonZeroAndLaunchFailure(t *testing.T) {
	launcher, _ := newLauncher(t, 4096)
	executable, _ := os.Executable()
	result, err := launcher.Run(context.Background(), helperRequest(executable, "exit", "7"))
	if err != nil {
		t.Fatal(err)
	}
	if result.ExitCode == nil || *result.ExitCode != 7 {
		t.Fatalf("unexpected result: %+v", result)
	}
	_, err = launcher.Run(context.Background(), Request{
		Executable: filepath.Join(t.TempDir(), "missing-executable"),
		CWD:        Directory{Root: "workspace", Path: "."},
	})
	if agenterr.CodeOf(err) != agenterr.LaunchFailed {
		t.Fatalf("launch error=%v", err)
	}
}

func TestRunResolvesRelativeAndBareExecutables(t *testing.T) {
	launcher, root := newLauncher(t, 4096)
	source, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	name := "cwd-helper"
	filename := name
	if runtime.GOOS == "windows" {
		filename += ".exe"
		t.Setenv("PATHEXT", ".COM;.EXE;.BAT;.CMD")
	}
	workingDirectory := filepath.Join(root, "requested")
	binDirectory := filepath.Join(workingDirectory, "bin")
	if err := os.MkdirAll(binDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	copyExecutable(t, source, filepath.Join(binDirectory, filename))

	t.Run("separator relative to requested cwd", func(t *testing.T) {
		request := helperRequest(filepath.Join("bin", name), "inspect")
		request.CWD.Path = "requested"
		result, err := launcher.Run(context.Background(), request)
		if err != nil {
			t.Fatal(err)
		}
		if result.ExitCode == nil || *result.ExitCode != 0 {
			t.Fatalf("unexpected exit: %+v", result)
		}
	})

	t.Run("bare through PATH", func(t *testing.T) {
		t.Setenv("PATH", binDirectory)
		request := helperRequest(name, "inspect")
		request.CWD.Path = "requested"
		result, err := launcher.Run(context.Background(), request)
		if err != nil {
			t.Fatal(err)
		}
		if result.ExitCode == nil || *result.ExitCode != 0 {
			t.Fatalf("unexpected exit: %+v", result)
		}
	})
}

func TestRunTimesOutAndCancellationStopsProcess(t *testing.T) {
	launcher, _ := newLauncher(t, 4096)
	executable, _ := os.Executable()
	timed, err := launcher.Run(context.Background(), Request{
		Executable:     executable,
		Arguments:      helperArguments("sleep", "30s"),
		CWD:            Directory{Root: "workspace", Path: "."},
		TimeoutSeconds: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !timed.TimedOut || timed.DurationMS < 900 {
		t.Fatalf("timeout result: %+v", timed)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan Result, 1)
	go func() {
		result, runErr := launcher.Run(ctx, helperRequest(executable, "sleep", "30s"))
		if runErr != nil {
			t.Errorf("run: %v", runErr)
		}
		done <- result
	}()
	time.Sleep(100 * time.Millisecond)
	cancel()
	select {
	case result := <-done:
		if result.Termination == nil {
			t.Fatalf("cancelled result has no termination: %+v", result)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cancelled command did not stop")
	}
}

func TestRunTruncatesEachStreamWhileDraining(t *testing.T) {
	launcher, _ := newLauncher(t, 32)
	executable, _ := os.Executable()
	result, err := launcher.Run(context.Background(), helperRequest(executable, "large-output"))
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Stdout) != 32 || len(result.Stderr) != 32 || !result.StdoutTruncated || !result.StderrTruncated {
		t.Fatalf("unexpected capture: %+v", result)
	}
}

func TestRunReplacesInvalidUTF8ForJSONText(t *testing.T) {
	launcher, _ := newLauncher(t, 32)
	executable, _ := os.Executable()
	result, err := launcher.Run(context.Background(), helperRequest(executable, "invalid-utf8"))
	if err != nil {
		t.Fatal(err)
	}
	if result.Stdout != "a\uFFFDb" || result.Stderr != "c\uFFFDd" {
		t.Fatalf("stdout=%q stderr=%q", result.Stdout, result.Stderr)
	}
}

func TestStartSurvivesCallerCancellationAndStopIsIdempotent(t *testing.T) {
	launcher, _ := newLauncher(t, 4096)
	executable, _ := os.Executable()
	ctx, cancel := context.WithCancel(context.Background())
	execution, err := launcher.Start(ctx, helperRequest(executable, "sleep", "30s"))
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	select {
	case <-execution.Done():
		t.Fatal("managed execution inherited caller cancellation")
	case <-time.After(100 * time.Millisecond):
	}
	var wg sync.WaitGroup
	for range 3 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := execution.Stop(context.Background()); err != nil {
				t.Errorf("stop: %v", err)
			}
		}()
	}
	wg.Wait()
	if _, err := execution.Wait(); err != nil {
		t.Fatal(err)
	}
}

func TestStopRecordsGracefulRequestBeforeLeaderExit(t *testing.T) {
	process := &orderedStopProcess{
		leaderExited: make(chan struct{}), signalStarted: make(chan struct{}),
		signalGate: make(chan struct{}), waitObserved: make(chan bool, 1),
		hardStopped: make(chan time.Time, 1), hardDone: make(chan struct{}),
	}
	launcher, _ := newLauncher(t, 1)
	launcher.startProcess = func(platform.Spec) (platform.OwnedProcess, *os.File, *os.File, error) {
		stdout, err := os.Open(os.DevNull)
		if err != nil {
			return nil, nil, nil, err
		}
		stderr, err := os.Open(os.DevNull)
		if err != nil {
			_ = stdout.Close()
			return nil, nil, nil, err
		}
		return process, stdout, stderr, nil
	}
	execution, err := launcher.Start(context.Background(), helperRequest("test", "sleep"))
	if err != nil {
		t.Fatal(err)
	}
	execution.grace = 100 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err = execution.Stop(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("stop error=%v", err)
	}
	close(process.leaderExited)
	select {
	case requested := <-process.waitObserved:
		if requested {
			break
		}
		close(process.signalGate)
		t.Fatal("leader exit was observed before the graceful request")
	case <-time.After(time.Second):
		t.Fatal("leader exit was not observed")
	}
	select {
	case <-process.signalStarted:
	case <-time.After(time.Second):
		t.Fatal("graceful signal did not start")
	}
	started := time.Now()
	close(process.signalGate)
	var stopped time.Time
	select {
	case stopped = <-process.hardStopped:
	case <-time.After(time.Second):
		t.Fatal("descendant was not hard-stopped after grace period")
	}
	if elapsed := stopped.Sub(started); elapsed < 80*time.Millisecond {
		t.Fatalf("descendant hard-stopped before grace period: %v", elapsed)
	}
	select {
	case <-execution.Done():
	case <-time.After(time.Second):
		t.Fatal("execution did not finish")
	}
}

type orderedStopProcess struct {
	graceful      atomic.Bool
	leaderExited  chan struct{}
	signalStarted chan struct{}
	signalGate    chan struct{}
	waitObserved  chan bool
	hardStopped   chan time.Time
	hardDone      chan struct{}
}

func (p *orderedStopProcess) RequestGracefulStop() {
	p.graceful.Store(true)
}

func (p *orderedStopProcess) Wait() (platform.Exit, error) {
	<-p.leaderExited
	requested := p.graceful.Load()
	p.waitObserved <- requested
	if requested {
		<-p.hardDone
	}
	return platform.Exit{}, nil
}

func (p *orderedStopProcess) GracefulStop() error {
	close(p.signalStarted)
	<-p.signalGate
	return nil
}

func (p *orderedStopProcess) HardStop() error {
	p.hardStopped <- time.Now()
	close(p.hardDone)
	return nil
}

func (p *orderedStopProcess) Close() error { return nil }

func TestLauncherShutdownStopsForegroundAndRefusesNewWork(t *testing.T) {
	launcher, _ := newLauncher(t, 4096)
	executable, _ := os.Executable()
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = launcher.Run(context.Background(), helperRequest(executable, "sleep", "30s"))
	}()
	time.Sleep(100 * time.Millisecond)
	if err := launcher.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("foreground command survived shutdown")
	}
	_, err := launcher.Run(context.Background(), helperRequest(executable, "inspect"))
	if agenterr.CodeOf(err) != agenterr.LaunchFailed {
		t.Fatalf("new run after shutdown: %v", err)
	}
}

func TestRequestValidationAndModeBoundary(t *testing.T) {
	launcher, root := newLauncher(t, 4096)
	executable, _ := os.Executable()
	tests := []Request{
		{CWD: Directory{Root: "workspace", Path: "."}},
		{Executable: "bad\x00name", CWD: Directory{Root: "workspace", Path: "."}},
		{Executable: string([]byte{0xff}), CWD: Directory{Root: "workspace", Path: "."}},
		{Executable: strings.Repeat("x", 4097), CWD: Directory{Root: "workspace", Path: "."}},
		{Executable: executable, Arguments: []string{"bad\x00argument"}, CWD: Directory{Root: "workspace", Path: "."}},
		{Executable: executable, Arguments: []string{strings.Repeat("x", (16<<10)+1)}, CWD: Directory{Root: "workspace", Path: "."}},
		{Executable: executable, Arguments: []string{strings.Repeat("x", 9000), strings.Repeat("y", 9000)}, CWD: Directory{Root: "workspace", Path: "."}},
		{Executable: executable, Arguments: make([]string, 257), CWD: Directory{Root: "workspace", Path: "."}},
		{Executable: executable, CWD: Directory{Root: "workspace", Path: "."}, TimeoutSeconds: -1},
		{Executable: executable, CWD: Directory{Root: "workspace", Path: "."}, TimeoutSeconds: 601},
	}
	for i, request := range tests {
		if _, err := launcher.Run(context.Background(), request); agenterr.CodeOf(err) != agenterr.InvalidInput {
			t.Errorf("case %d: %v", i, err)
		}
	}
	readonly, _ := newLauncherMode(t, config.Readonly, 4096)
	_, err := readonly.Run(context.Background(), helperRequest(executable, "inspect"))
	if agenterr.CodeOf(err) != agenterr.ModeDenied {
		t.Fatalf("readonly run: %v", err)
	}
	if err := os.Symlink(t.TempDir(), filepath.Join(root, "escape")); err == nil {
		request := helperRequest(executable, "inspect")
		request.CWD.Path = "escape"
		_, err = launcher.Run(context.Background(), request)
		if agenterr.CodeOf(err) != agenterr.PathDenied {
			t.Fatalf("escaping cwd: %v", err)
		}
	}
}

type helperInspection struct {
	Args        []string          `json:"args"`
	Directory   string            `json:"directory"`
	Environment map[string]string `json:"environment"`
}

func TestHelperProcess(t *testing.T) {
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
	case "inspect":
		directory, _ := os.Getwd()
		environment := make(map[string]string)
		for _, name := range []string{"LANG", "SystemRoot", "COMPUTER_AGENT_TEST_SECRET", "OPENAI_API_KEY"} {
			if value, ok := os.LookupEnv(name); ok {
				environment[name] = value
			}
		}
		_ = json.NewEncoder(os.Stdout).Encode(helperInspection{Args: arguments[1:], Directory: directory, Environment: environment})
		_, _ = fmt.Fprint(os.Stderr, "stderr: 雪☃")
	case "exit":
		code, _ := strconv.Atoi(arguments[1])
		os.Exit(code)
	case "sleep":
		duration, _ := time.ParseDuration(arguments[1])
		time.Sleep(duration)
	case "large-output":
		_, _ = fmt.Fprint(os.Stdout, strings.Repeat("o", 100_000))
		_, _ = fmt.Fprint(os.Stderr, strings.Repeat("e", 100_000))
	case "invalid-utf8":
		_, _ = os.Stdout.Write([]byte{'a', 0xff, 'b'})
		_, _ = os.Stderr.Write([]byte{'c', 0xff, 'd'})
	case "spawn-and-sleep":
		child := exec.Command(os.Args[0], "-test.run=TestHelperProcess", "--", "sleep", "30s")
		if err := child.Start(); err != nil {
			_, _ = fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		_, _ = fmt.Fprintln(os.Stdout, child.Process.Pid)
		time.Sleep(30 * time.Second)
	case "spawn-marker-and-sleep":
		child := exec.Command(os.Args[0], "-test.run=TestHelperProcess", "--", "sleep", "30s")
		if err := child.Start(); err != nil {
			_, _ = fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		if err := os.WriteFile(arguments[1], []byte(strconv.Itoa(child.Process.Pid)), 0o600); err != nil {
			_, _ = fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		_, _ = fmt.Fprintln(os.Stdout, child.Process.Pid)
		time.Sleep(30 * time.Second)
	}
	os.Exit(0)
}

func helperRequest(executable, mode string, arguments ...string) Request {
	return Request{
		Executable: executable,
		Arguments:  helperArguments(mode, arguments...),
		CWD:        Directory{Root: "workspace", Path: "."},
	}
}

func helperArguments(mode string, arguments ...string) []string {
	return append([]string{"-test.run=TestHelperProcess", "--", mode}, arguments...)
}

func copyExecutable(t *testing.T, source, destination string) {
	t.Helper()
	data, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(destination, data, 0o700); err != nil {
		t.Fatal(err)
	}
}

func newLauncher(t *testing.T, outputLimit int) (*Launcher, string) {
	return newLauncherMode(t, config.UserShell, outputLimit)
}

func newLauncherMode(t *testing.T, mode config.Mode, outputLimit int) (*Launcher, string) {
	t.Helper()
	return newLauncherModeAt(t, mode, outputLimit, t.TempDir())
}

func newLauncherModeAt(t *testing.T, mode config.Mode, outputLimit int, root string) (*Launcher, string) {
	t.Helper()
	configPath := filepath.Join(t.TempDir(), "config.json")
	document := map[string]any{
		"version": 1,
		"mode":    mode,
		"roots":   []map[string]string{{"name": "workspace", "path": root}},
		"limits": map[string]int{
			"default_command_timeout_seconds": 30,
			"max_command_timeout_seconds":     600,
			"max_output_bytes_per_stream":     outputLimit,
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
	t.Cleanup(func() { _ = roots.Close() })
	launcher := New(roots, cfg.Limits())
	t.Cleanup(func() { _ = launcher.Shutdown(context.Background()) })
	return launcher, root
}
