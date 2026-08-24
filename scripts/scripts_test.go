package scripts

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
)

func TestScriptSyntax(t *testing.T) {
	root := repositoryRoot(t)
	if runtime.GOOS != "windows" {
		for _, name := range []string{"check.sh", "build-release.sh", "install.sh"} {
			path := filepath.Join(root, "scripts", name)
			command := exec.Command("bash", "-n", path)
			if output, err := command.CombinedOutput(); err != nil {
				t.Fatalf("%s: %v\n%s", name, err, output)
			}
		}
	}
	shell, err := lookPathPowerShell()
	if err != nil {
		if runtime.GOOS == "windows" {
			t.Fatalf("native PowerShell is required to validate install.ps1: %v", err)
		}
		t.Log("pwsh is unavailable; PowerShell syntax runs on the Windows CI job")
		return
	}
	path := filepath.Join(root, "scripts", "install.ps1")
	quoted := "'" + strings.ReplaceAll(path, "'", "''") + "'"
	command := exec.Command(shell, "-NoProfile", "-NonInteractive", "-Command",
		"[void][ScriptBlock]::Create((Get-Content -Raw -LiteralPath "+quoted+"))")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("install.ps1: %v\n%s", err, output)
	}
}

func lookPathPowerShell() (string, error) {
	if path, err := exec.LookPath("pwsh"); err == nil {
		return path, nil
	}
	return exec.LookPath("powershell")
}

func TestPowerShellInstallerPathFixture(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("PowerShell installer PATH fixture runs on Windows CI")
	}
	root := repositoryRoot(t)
	shell, err := lookPathPowerShell()
	if err != nil {
		t.Fatal(err)
	}
	fixture := filepath.Join(root, "scripts", "install_path_test.ps1")
	installer := filepath.Join(root, "scripts", "install.ps1")
	command := exec.Command(shell, "-NoProfile", "-NonInteractive", "-File", fixture, "-Installer", installer)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("PowerShell installer PATH fixture: %v\n%s", err, output)
	}
}

func TestShellInstallerVerifiesChecksumAndPreservesConfiguration(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell installer fixture runs on POSIX; PowerShell runs on Windows CI")
	}
	root := repositoryRoot(t)
	fixture := t.TempDir()
	installDirectory := filepath.Join(t.TempDir(), "bin")
	configDirectory := filepath.Join(t.TempDir(), "config")
	if err := os.MkdirAll(configDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(configDirectory, "config.json")
	if err := os.WriteFile(configPath, []byte("preserve"), 0o600); err != nil {
		t.Fatal(err)
	}
	name := fmt.Sprintf("chatgpt-computer-agent-mcp-%s-%s", runtime.GOOS, runtime.GOARCH)
	binary := []byte("#!/bin/sh\necho fixture\n")
	if err := os.WriteFile(filepath.Join(fixture, name), binary, 0o755); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(binary)
	checksums := hex.EncodeToString(sum[:]) + "  " + name + "\n"
	checksumPath := filepath.Join(fixture, "SHA256SUMS")
	if err := os.WriteFile(checksumPath, []byte(checksums), 0o600); err != nil {
		t.Fatal(err)
	}
	fakeBin := t.TempDir()
	fakeCurl := `#!/bin/sh
set -eu
output=""
url=""
while [ "$#" -gt 0 ]; do
  case "$1" in
    -o) output="$2"; shift 2 ;;
    http*) url="$1"; shift ;;
    *) shift ;;
  esac
done
cp "$FAKE_RELEASE_DIR/${url##*/}" "$output"
`
	if err := os.WriteFile(filepath.Join(fakeBin, "curl"), []byte(fakeCurl), 0o755); err != nil {
		t.Fatal(err)
	}
	environment := append(os.Environ(),
		"PATH="+fakeBin+string(os.PathListSeparator)+os.Getenv("PATH"),
		"FAKE_RELEASE_DIR="+fixture,
		"COMPUTER_AGENT_REPOSITORY=example/project",
		"COMPUTER_AGENT_VERSION=v1.0.0",
		"COMPUTER_AGENT_INSTALL_DIR="+installDirectory,
		"XDG_CONFIG_HOME="+configDirectory,
	)
	installer := filepath.Join(root, "scripts", "install.sh")
	output := runScriptOutput(t, environment, installer)
	if !strings.Contains(output, installDirectory+" is not on your PATH.") ||
		!strings.Contains(output, "Add the same line to your shell profile") {
		t.Fatalf("missing PATH guidance: %q", output)
	}
	target := filepath.Join(installDirectory, "computer-agent")
	installed, err := os.ReadFile(target)
	if err != nil || string(installed) != string(binary) {
		t.Fatalf("installed=%q err=%v", installed, err)
	}
	environmentWithInstallPath := append([]string{}, environment...)
	for index, variable := range environmentWithInstallPath {
		if strings.HasPrefix(variable, "PATH=") {
			environmentWithInstallPath[index] = "PATH=" + installDirectory + string(os.PathListSeparator) + strings.TrimPrefix(variable, "PATH=")
		}
	}
	output = runScriptOutput(t, environmentWithInstallPath, installer)
	if !strings.Contains(output, "You can now run: computer-agent") || strings.Contains(output, "is not on your PATH") {
		t.Fatalf("incorrect output when install directory is on PATH: %q", output)
	}

	if err := os.WriteFile(checksumPath, []byte(strings.Repeat("0", 64)+"  "+name+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	command := exec.Command("bash", installer)
	command.Env = environment
	if err := command.Run(); err == nil {
		t.Fatal("installer accepted a bad checksum")
	}
	installed, err = os.ReadFile(target)
	if err != nil || string(installed) != string(binary) {
		t.Fatalf("failed verification changed install: %q %v", installed, err)
	}

	runScript(t, environment, installer, "--uninstall")
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("binary remains after uninstall: %v", err)
	}
	if data, err := os.ReadFile(configPath); err != nil || string(data) != "preserve" {
		t.Fatalf("configuration changed: %q %v", data, err)
	}
}

func TestReleaseBuilderProducesExactlySixChecksummedBinaries(t *testing.T) {
	if os.Getenv("RUN_RELEASE_SCRIPT_TEST") != "1" {
		t.Skip("set RUN_RELEASE_SCRIPT_TEST=1 for the six-target release fixture")
	}
	root := repositoryRoot(t)
	outputDirectory := t.TempDir()
	command := exec.Command("bash", filepath.Join(root, "scripts", "build-release.sh"))
	command.Dir = root
	command.Env = append(os.Environ(), "VERSION=v0.0.0-test", "OUTPUT_DIR="+outputDirectory)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("build release: %v\n%s", err, output)
	}
	entries, err := os.ReadDir(outputDirectory)
	if err != nil {
		t.Fatal(err)
	}
	got := make([]string, 0, len(entries))
	for _, entry := range entries {
		got = append(got, entry.Name())
	}
	sort.Strings(got)
	want := []string{
		"LICENSE", "SHA256SUMS", "THIRD_PARTY_NOTICES",
		"chatgpt-computer-agent-mcp-darwin-amd64", "chatgpt-computer-agent-mcp-darwin-arm64",
		"chatgpt-computer-agent-mcp-linux-amd64", "chatgpt-computer-agent-mcp-linux-arm64",
		"chatgpt-computer-agent-mcp-windows-amd64.exe", "chatgpt-computer-agent-mcp-windows-arm64.exe",
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("artifacts=%v want=%v", got, want)
	}
	checksums, err := os.ReadFile(filepath.Join(outputDirectory, "SHA256SUMS"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(strings.TrimSpace(string(checksums)), "\n")+1 != 8 {
		t.Fatalf("checksums=%q", checksums)
	}
}

func TestReleasePublicationRequiresSameSHAValidation(t *testing.T) {
	root := repositoryRoot(t)
	workflows, err := filepath.Glob(filepath.Join(root, ".github", "workflows", "*.yml"))
	if err != nil {
		t.Fatal(err)
	}
	publishers := 0
	var document string
	for _, path := range workflows {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		publishers += strings.Count(string(data), "softprops/action-gh-release@")
		if filepath.Base(path) == "ci.yml" {
			document = string(data)
		}
	}
	if publishers != 1 {
		t.Fatalf("release publishers=%d want=1", publishers)
	}
	if !strings.Contains(document, "tags: [\"v*\"]") {
		t.Fatal("CI workflow does not validate v* tag pushes")
	}

	jobs := map[string][]string{
		"quality":       {"ref: ${{ github.sha }}", "./scripts/check.sh"},
		"native":        {"ref: ${{ github.sha }}", "go test ./...", "go test -race ./...", "go vet ./..."},
		"secret-scan":   {"ref: ${{ github.sha }}", "fetch-depth: 0", "gitleaks/gitleaks-action@"},
		"release-build": {"if: startsWith(github.ref, 'refs/tags/v')", "needs: [quality, native, secret-scan]", "ref: ${{ github.sha }}", "./scripts/build-release.sh"},
		"publish":       {"if: startsWith(github.ref, 'refs/tags/v')", "needs: [quality, native, secret-scan, release-build]", "contents: write", "softprops/action-gh-release@"},
	}
	for name, fragments := range jobs {
		job := workflowJob(t, document, name)
		for _, fragment := range fragments {
			if !strings.Contains(job, fragment) {
				t.Errorf("job %s is missing %q", name, fragment)
			}
		}
	}
	for _, name := range []string{"quality", "native", "secret-scan", "release-build"} {
		if strings.Contains(workflowJob(t, document, name), "contents: write") {
			t.Errorf("job %s has publication permission", name)
		}
	}
}

func workflowJob(t *testing.T, document, name string) string {
	t.Helper()
	document = strings.ReplaceAll(document, "\r\n", "\n")
	document = strings.ReplaceAll(document, "\r", "\n")
	lines := strings.Split(document, "\n")
	start := -1
	for i, line := range lines {
		if line == "  "+name+":" {
			start = i
			continue
		}
		if start >= 0 && strings.HasPrefix(line, "  ") && !strings.HasPrefix(line, "    ") && strings.HasSuffix(line, ":") {
			return strings.Join(lines[start:i], "\n")
		}
	}
	if start >= 0 {
		return strings.Join(lines[start:], "\n")
	}
	t.Fatalf("workflow job %q is missing", name)
	return ""
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	directory, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Dir(directory)
}

func runScript(t *testing.T, environment []string, path string, arguments ...string) {
	t.Helper()
	runScriptOutput(t, environment, path, arguments...)
}

func runScriptOutput(t *testing.T, environment []string, path string, arguments ...string) string {
	t.Helper()
	command := exec.Command("bash", append([]string{path}, arguments...)...)
	command.Env = environment
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("%s: %v\n%s", filepath.Base(path), err, output)
	}
	return string(output)
}
