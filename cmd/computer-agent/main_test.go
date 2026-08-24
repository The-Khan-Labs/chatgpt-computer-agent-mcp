package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestRunHelpAndVersion(t *testing.T) {
	previous := version
	version = "1.2.3-test"
	t.Cleanup(func() { version = previous })
	for _, test := range []struct {
		args []string
		want string
	}{
		{[]string{"--help"}, "Usage: computer-agent"},
		{[]string{"--version"}, "computer-agent 1.2.3-test\n"},
	} {
		var stdout, stderr bytes.Buffer
		if code := run(test.args, &stdout, &stderr); code != 0 {
			t.Fatalf("args=%v code=%d stderr=%q", test.args, code, stderr.String())
		}
		if !strings.Contains(stdout.String(), test.want) || stderr.Len() != 0 {
			t.Fatalf("args=%v stdout=%q stderr=%q", test.args, stdout.String(), stderr.String())
		}
	}
}

func TestRunReportsMissingConfigurationWithoutUsingStdout(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing.json")
	var stdout, stderr bytes.Buffer
	if code := run([]string{"--config", missing}, &stdout, &stderr); code == 0 {
		t.Fatal("missing config succeeded")
	}
	if stdout.Len() != 0 || !containsPath(stderr.String(), missing) || !strings.Contains(stderr.String(), "--config") {
		t.Fatalf("stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
}

func TestRunRejectsPositionalArguments(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run([]string{"unexpected"}, &stdout, &stderr); code == 0 {
		t.Fatal("positional argument succeeded")
	}
	if stdout.Len() != 0 || !strings.Contains(stderr.String(), "unexpected positional") {
		t.Fatalf("stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
}

func TestStdioEOFExitsCleanlyWithoutWritingDiagnosticsToStdout(t *testing.T) {
	configPath, _ := writeCLIConfig(t, "readonly")
	command := cliSubprocess(t, "--config", configPath)
	command.Stdin = strings.NewReader("")
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		t.Fatalf("run: %v stderr=%q", err, stderr.String())
	}
	if stdout.Len() != 0 || stderr.Len() != 0 {
		t.Fatalf("stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
}

func TestCLIEntrypoint(t *testing.T) {
	if os.Getenv("COMPUTER_AGENT_CLI_TEST") != "1" {
		return
	}
	separator := -1
	for i, argument := range os.Args {
		if argument == "--" {
			separator = i
			break
		}
	}
	if separator < 0 {
		os.Exit(2)
	}
	os.Exit(run(os.Args[separator+1:], os.Stdout, os.Stderr))
}

func cliSubprocess(t *testing.T, arguments ...string) *exec.Cmd {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	args := append([]string{"-test.run=TestCLIEntrypoint", "--"}, arguments...)
	command := exec.Command(executable, args...)
	command.Env = append(os.Environ(), "COMPUTER_AGENT_CLI_TEST=1")
	return command
}

func writeCLIConfig(t *testing.T, mode string) (string, string) {
	t.Helper()
	root := t.TempDir()
	path := filepath.Join(t.TempDir(), "config.json")
	document := map[string]any{
		"version": 1, "mode": mode,
		"roots": []map[string]string{{"name": "workspace", "path": root}},
		"limits": map[string]int{
			"default_command_timeout_seconds": 30, "process_stop_grace_seconds": 1,
			"max_output_bytes_per_stream": 1024, "max_background_processes": 2,
		},
	}
	data, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return path, root
}

func containsPath(text, path string) bool {
	return strings.Contains(text, path) || strings.Contains(text, strconv.Quote(path))
}
