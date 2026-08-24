//go:build windows

package command

import (
	"context"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

func TestWindowsManagedStopAllowsDescendantToFinishWithinGracePeriod(t *testing.T) {
	launcher, root := newLauncher(t, 4096)
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ready := filepath.Join(root, "graceful-child.ready")
	leaderStopped := filepath.Join(root, "graceful-leader.stopped")
	finished := filepath.Join(root, "graceful-child.finished")
	execution, err := launcher.Start(context.Background(), Request{
		Executable: executable,
		Arguments: []string{
			"-test.run=^TestWindowsGracefulStopHelper$", "--", ready, leaderStopped, finished,
		},
		CWD: Directory{Root: "workspace", Path: "."},
	})
	if err != nil {
		t.Fatal(err)
	}
	waitForWindowsFile(t, ready)
	if err := execution.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{leaderStopped, finished} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("graceful marker %s was not written: %v", filepath.Base(path), err)
		}
	}
}

func TestWindowsGracefulStopHelper(t *testing.T) {
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
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt)
	child := exec.Command(os.Args[0], "-test.run=^TestWindowsGracefulChildHelper$", "--", arguments[0], arguments[2])
	child.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NEW_PROCESS_GROUP}
	if err := child.Start(); err != nil {
		os.Exit(2)
	}
	waitForWindowsHelperFile(arguments[0])
	<-signals
	if err := os.WriteFile(arguments[1], nil, 0o600); err != nil {
		os.Exit(2)
	}
	os.Exit(0)
}

func TestWindowsGracefulChildHelper(t *testing.T) {
	separator := -1
	for i, argument := range os.Args {
		if argument == "--" {
			separator = i
			break
		}
	}
	if separator < 0 || separator+2 >= len(os.Args) {
		return
	}
	arguments := os.Args[separator+1:]
	if err := os.WriteFile(arguments[0], nil, 0o600); err != nil {
		os.Exit(2)
	}
	time.Sleep(200 * time.Millisecond)
	if err := os.WriteFile(arguments[1], nil, 0o600); err != nil {
		os.Exit(2)
	}
	os.Exit(0)
}

func waitForWindowsFile(t *testing.T, path string) {
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

func waitForWindowsHelperFile(path string) {
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	os.Exit(2)
}
