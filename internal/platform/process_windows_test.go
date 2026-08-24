//go:build windows

package platform

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

func TestWindowsStartUsesPipesAndReportsExit(t *testing.T) {
	directory := t.TempDir()
	process, stdout, stderr, err := Start(Spec{
		Executable:        os.Getenv("ComSpec"),
		Arguments:         []string{"/d", "/c", "echo out & echo err 1>&2"},
		Directory:         directory,
		DirectoryIdentity: testDirectoryIdentity(t, directory),
		Environment:       []string{"SystemRoot=" + os.Getenv("SystemRoot"), "ComSpec=" + os.Getenv("ComSpec")},
	})
	if err != nil {
		t.Fatal(err)
	}
	outDone := make(chan []byte, 1)
	errDone := make(chan []byte, 1)
	go func() { data, _ := io.ReadAll(stdout); outDone <- data }()
	go func() { data, _ := io.ReadAll(stderr); errDone <- data }()
	exit, err := process.Wait()
	if err != nil || exit.ExitCode == nil || *exit.ExitCode != 0 {
		t.Fatalf("wait: exit=%+v err=%v", exit, err)
	}
	if len(<-outDone) == 0 || len(<-errDone) == 0 {
		t.Fatal("expected separate stdout and stderr")
	}
	if err := process.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestWindowsRejectsNativeCommandLineOverflow(t *testing.T) {
	_, _, _, err := Start(Spec{
		Executable: `C:\Windows\System32\cmd.exe`, Arguments: []string{string(make([]byte, 32768))},
		Directory: `C:\`, DirectoryIdentity: testDirectoryIdentity(t, `C:\`),
	})
	if err == nil {
		t.Fatal("expected native command-line length error")
	}
}

func TestWindowsJobStopsDescendantProcess(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	process, stdout, stderr, err := Start(Spec{
		Executable: executable,
		Arguments:  []string{"-test.run=TestWindowsPlatformHelper", "--", "spawn-and-sleep"},
		Directory:  directory, DirectoryIdentity: testDirectoryIdentity(t, directory),
		Environment: []string{
			"Path=" + os.Getenv("Path"), "SystemRoot=" + os.Getenv("SystemRoot"),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = process.Close() }()
	go func() { _, _ = io.Copy(io.Discard, stderr) }()
	line, err := bufio.NewReader(stdout).ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(line))
	if err != nil {
		t.Fatal(err)
	}
	if err := process.HardStop(); err != nil {
		t.Fatal(err)
	}
	if _, err := process.Wait(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if !windowsProcessAlive(uint32(pid)) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("descendant %d survived Job Object termination", pid)
}

func TestWindowsPlatformHelper(t *testing.T) {
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
	if os.Args[separator+1] == "spawn-and-sleep" {
		child := exec.Command(os.Args[0], "-test.run=TestWindowsPlatformHelper", "--", "sleep")
		if err := child.Start(); err != nil {
			_, _ = fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		_, _ = fmt.Fprintln(os.Stdout, child.Process.Pid)
	}
	time.Sleep(30 * time.Second)
}

func windowsProcessAlive(pid uint32) bool {
	handle, err := windows.OpenProcess(windows.SYNCHRONIZE, false, pid)
	if err != nil {
		return !errors.Is(err, windows.ERROR_INVALID_PARAMETER)
	}
	defer func() { _ = windows.CloseHandle(handle) }()
	status, err := windows.WaitForSingleObject(handle, 0)
	return err == nil && status == uint32(windows.WAIT_TIMEOUT)
}

func testDirectoryIdentity(t *testing.T, directory string) os.FileInfo {
	t.Helper()
	info, err := os.Stat(directory)
	if err != nil {
		t.Fatal(err)
	}
	return info
}
