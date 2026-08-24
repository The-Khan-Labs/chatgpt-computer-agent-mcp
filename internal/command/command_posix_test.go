//go:build linux || darwin

package command

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"chatgpt-computer-agent-mcp/internal/agenterr"
	"chatgpt-computer-agent-mcp/internal/platform"
)

func TestManagedStopAllowsDescendantToFinishWithinGracePeriod(t *testing.T) {
	launcher, root := newLauncher(t, 4096)
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ready := filepath.Join(root, "graceful-child.ready")
	finished := filepath.Join(root, "graceful-child.finished")
	execution, err := launcher.Start(context.Background(), Request{
		Executable: executable,
		Arguments: []string{
			"-test.run=^TestGracefulStopHelper$", "--", "leader", ready, finished,
		},
		CWD: Directory{Root: "workspace", Path: "."},
	})
	if err != nil {
		t.Fatal(err)
	}
	waitForFile(t, ready)
	if err := execution.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(finished); err != nil {
		t.Fatalf("descendant did not finish during grace period: %v", err)
	}
}

func TestLaunchRejectsCWDReplacedAfterAuthorization(t *testing.T) {
	launcher, root := newLauncher(t, 4096)
	approved := filepath.Join(root, "approved")
	if err := os.Mkdir(approved, 0o700); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	launcher.startProcess = func(spec platform.Spec) (platform.OwnedProcess, *os.File, *os.File, error) {
		if err := os.Rename(approved, approved+"-original"); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(outside, approved); err != nil {
			t.Fatal(err)
		}
		return platform.Start(spec)
	}
	request := helperRequest(executable, "inspect")
	request.CWD.Path = "approved"
	result, err := launcher.Run(context.Background(), request)
	if agenterr.CodeOf(err) != agenterr.LaunchFailed {
		t.Fatalf("replacement cwd launch result=%+v error=%v", result, err)
	}
}

func TestGracefulStopHelper(t *testing.T) {
	separator := -1
	for i, argument := range os.Args {
		if argument == "--" {
			separator = i
			break
		}
	}
	if separator < 0 || separator+3 >= len(os.Args) {
		return
	}
	arguments := os.Args[separator+1:]
	if arguments[0] == "leader" {
		child := exec.Command(os.Args[0], "-test.run=^TestGracefulStopHelper$", "--", "child", arguments[1], arguments[2])
		if err := child.Start(); err != nil {
			os.Exit(2)
		}
		waitForHelperFile(arguments[1])
		time.Sleep(30 * time.Second)
	}

	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGTERM)
	if err := os.WriteFile(arguments[1], nil, 0o600); err != nil {
		os.Exit(2)
	}
	<-signals
	time.Sleep(200 * time.Millisecond)
	if err := os.WriteFile(arguments[2], nil, 0o600); err != nil {
		os.Exit(2)
	}
	os.Exit(0)
}

func TestRunTimeoutKillsDescendantProcess(t *testing.T) {
	launcher, _ := newLauncher(t, 4096)
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	result, err := launcher.Run(context.Background(), Request{
		Executable: executable, Arguments: helperArguments("spawn-and-sleep"),
		CWD: Directory{Root: "workspace", Path: "."}, TimeoutSeconds: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(result.Stdout))
	if err != nil {
		t.Fatalf("stdout=%q: %v", result.Stdout, err)
	}
	assertProcessStops(t, pid)
}

func TestRunCancellationKillsDescendantProcess(t *testing.T) {
	launcher, root := newLauncher(t, 4096)
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(root, "cancelled-child.pid")
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan Result, 1)
	go func() {
		result, runErr := launcher.Run(ctx, helperRequest(executable, "spawn-marker-and-sleep", marker))
		if runErr != nil {
			t.Errorf("run: %v", runErr)
		}
		done <- result
	}()
	pid := waitPIDFile(t, marker)
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("cancelled command did not finish")
	}
	assertProcessStops(t, pid)
}

func TestManagedStopKillsDescendantProcess(t *testing.T) {
	launcher, root := newLauncher(t, 4096)
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(root, "stopped-child.pid")
	execution, err := launcher.Start(context.Background(), helperRequest(executable, "spawn-marker-and-sleep", marker))
	if err != nil {
		t.Fatal(err)
	}
	pid := waitPIDFile(t, marker)
	if err := execution.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	assertProcessStops(t, pid)
}

func TestLauncherShutdownKillsForegroundDescendantProcess(t *testing.T) {
	launcher, root := newLauncher(t, 4096)
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(root, "shutdown-child.pid")
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = launcher.Run(context.Background(), helperRequest(executable, "spawn-marker-and-sleep", marker))
	}()
	pid := waitPIDFile(t, marker)
	if err := launcher.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("foreground command survived launcher shutdown")
	}
	assertProcessStops(t, pid)
}

func waitPIDFile(t *testing.T, marker string) int {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(marker)
		if err == nil {
			pid, parseErr := strconv.Atoi(strings.TrimSpace(string(data)))
			if parseErr == nil {
				return pid
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("child PID marker was not written: %s", marker)
	return 0
}

func waitForFile(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("file was not written: %s", path)
}

func waitForHelperFile(path string) {
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	os.Exit(2)
}

func assertProcessStops(t *testing.T, pid int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if !commandProcessAlive(pid) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("descendant %d survived cleanup", pid)
}

func commandProcessAlive(pid int) bool {
	if err := syscall.Kill(pid, 0); err != nil {
		return false
	}
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err == nil {
		fields := strings.Fields(string(data))
		return len(fields) < 3 || fields[2] != "Z"
	}
	return true
}
