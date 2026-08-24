//go:build linux || darwin

package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestSIGTERMStopsStdioServerCleanly(t *testing.T) {
	configPath, _ := writeCLIConfig(t, "readonly")
	command := cliSubprocess(t, "--config", configPath)
	stdin, err := command.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = stdin.Close() }()
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	time.Sleep(150 * time.Millisecond)
	if err := command.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	if err := command.Wait(); err != nil {
		t.Fatalf("wait: %v stderr=%q", err, stderr.String())
	}
	if stdout.Len() != 0 || stderr.Len() != 0 {
		t.Fatalf("stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
}

func TestStdioEOFKillsManagedProcess(t *testing.T) {
	configPath, root := writeCLIConfig(t, "user-shell")
	marker := root + string(os.PathSeparator) + "managed.pid"
	command := cliSubprocess(t, "--config", configPath)
	stdin, err := command.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	command.Stderr = &stderr
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	scanner := bufio.NewScanner(stdout)
	writeRPC(t, stdin, map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "initialize",
		"params": map[string]any{
			"protocolVersion": "2025-06-18", "capabilities": map[string]any{},
			"clientInfo": map[string]any{"name": "test", "version": "1"},
		},
	})
	readRPC(t, scanner, 1)
	writeRPC(t, stdin, map[string]any{"jsonrpc": "2.0", "method": "notifications/initialized"})
	executable, _ := os.Executable()
	writeRPC(t, stdin, map[string]any{
		"jsonrpc": "2.0", "id": 2, "method": "tools/call",
		"params": map[string]any{
			"name": "process_start",
			"arguments": map[string]any{
				"executable": executable,
				"arguments":  []string{"-test.run=TestCLIManagedHelper", "--", marker},
				"cwd":        map[string]any{"root": "workspace", "path": "."},
			},
		},
	})
	response := readRPC(t, scanner, 2)
	result, _ := response["result"].(map[string]any)
	if result["isError"] == true {
		t.Fatalf("process_start response: %#v", response)
	}
	deadline := time.Now().Add(5 * time.Second)
	var pid int
	for time.Now().Before(deadline) {
		data, readErr := os.ReadFile(marker)
		if readErr == nil {
			pid, err = strconv.Atoi(strings.TrimSpace(string(data)))
			if err == nil {
				break
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	if pid == 0 {
		t.Fatal("managed helper did not write its PID")
	}
	if err := stdin.Close(); err != nil {
		t.Fatal(err)
	}
	if err := command.Wait(); err != nil {
		t.Fatalf("wait: %v stderr=%q", err, stderr.String())
	}
	deadline = time.Now().Add(2 * time.Second)
	for cliProcessAlive(pid) && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if cliProcessAlive(pid) {
		t.Fatalf("managed process %d survived stdio EOF", pid)
	}
}

func TestCLIManagedHelper(t *testing.T) {
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
	child := exec.Command(os.Args[0], "-test.run=TestCLIManagedChild", "--", "sleep")
	if err := child.Start(); err != nil {
		os.Exit(2)
	}
	if err := os.WriteFile(os.Args[separator+1], []byte(strconv.Itoa(child.Process.Pid)), 0o600); err != nil {
		os.Exit(2)
	}
	time.Sleep(30 * time.Second)
	os.Exit(0)
}

func TestCLIManagedChild(t *testing.T) {
	for _, argument := range os.Args {
		if argument == "--" {
			time.Sleep(30 * time.Second)
			return
		}
	}
}

func writeRPC(t *testing.T, writer interface{ Write([]byte) (int, error) }, message map[string]any) {
	t.Helper()
	data, err := json.Marshal(message)
	if err != nil {
		t.Fatal(err)
	}
	data = append(data, '\n')
	if _, err := writer.Write(data); err != nil {
		t.Fatal(err)
	}
}

func readRPC(t *testing.T, scanner *bufio.Scanner, id int) map[string]any {
	t.Helper()
	if !scanner.Scan() {
		t.Fatalf("missing RPC response %d: %v", id, scanner.Err())
	}
	var response map[string]any
	if err := json.Unmarshal(scanner.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response["id"] != float64(id) {
		t.Fatalf("response id=%v want=%d: %#v", response["id"], id, response)
	}
	return response
}

func cliProcessAlive(pid int) bool {
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
