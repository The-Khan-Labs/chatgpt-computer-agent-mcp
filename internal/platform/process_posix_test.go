//go:build !windows

package platform

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestStartWaitKillsDescendantsWhenLeaderExits(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	process, stdout, stderr, err := Start(Spec{
		Executable: executable,
		Arguments:  []string{"-test.run=TestPlatformHelper", "--", "spawn-and-exit"},
		Directory:  directory, DirectoryIdentity: testDirectoryIdentity(t, directory),
		Environment: baselineTestEnvironment(),
	})
	if err != nil {
		t.Fatal(err)
	}
	stderrDone := make(chan struct{})
	go func() { _, _ = io.Copy(io.Discard, stderr); close(stderrDone) }()
	line, err := bufio.NewReader(stdout).ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(line))
	if err != nil {
		t.Fatal(err)
	}
	exit, err := process.Wait()
	if err != nil || exit.ExitCode == nil || *exit.ExitCode != 0 {
		t.Fatalf("wait: exit=%+v err=%v", exit, err)
	}
	<-stderrDone
	_ = process.Close()
	deadline := time.Now().Add(2 * time.Second)
	for processAlive(pid) && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if processAlive(pid) {
		t.Fatalf("descendant %d survived leader exit", pid)
	}
}

func testDirectoryIdentity(t *testing.T, directory string) os.FileInfo {
	t.Helper()
	info, err := os.Stat(directory)
	if err != nil {
		t.Fatal(err)
	}
	return info
}

func TestPlatformHelper(t *testing.T) {
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
	switch os.Args[separator+1] {
	case "spawn-and-exit":
		child := exec.Command(os.Args[0], "-test.run=TestPlatformHelper", "--", "sleep")
		if err := child.Start(); err != nil {
			_, _ = fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		_, _ = fmt.Fprintln(os.Stdout, child.Process.Pid)
	case "sleep":
		time.Sleep(30 * time.Second)
	}
	os.Exit(0)
}

func baselineTestEnvironment() []string {
	result := []string{}
	for _, name := range []string{"PATH", "HOME", "TMPDIR"} {
		if value, ok := os.LookupEnv(name); ok {
			result = append(result, name+"="+value)
		}
	}
	return result
}

func processAlive(pid int) bool {
	err := syscall.Kill(pid, 0)
	if err != nil {
		return false
	}
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err == nil {
		fields := strings.Fields(string(data))
		return len(fields) < 3 || fields[2] != "Z"
	}
	return true
}
