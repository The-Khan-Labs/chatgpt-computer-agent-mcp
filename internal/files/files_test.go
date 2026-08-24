package files

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"chatgpt-computer-agent-mcp/internal/agenterr"
	"chatgpt-computer-agent-mcp/internal/config"
	"chatgpt-computer-agent-mcp/internal/policy"
)

func TestReadFileValidatesTextAndBounds(t *testing.T) {
	service, root := newService(t, config.Readonly, 32, 64)
	mustWrite(t, filepath.Join(root, "hello 世界.txt"), []byte("hello, 世界"), 0o600)

	result, err := service.Read(ReadRequest{Root: "workspace", Path: "hello 世界.txt"})
	if err != nil {
		t.Fatal(err)
	}
	wantHash := sha256.Sum256([]byte("hello, 世界"))
	if result.Content != "hello, 世界" || result.Bytes != len("hello, 世界") || result.SHA256 != hex.EncodeToString(wantHash[:]) {
		t.Fatalf("unexpected result: %+v", result)
	}

	tests := []struct {
		name string
		path string
		data []byte
		max  int
		code agenterr.Code
	}{
		{name: "requested limit", path: "large.txt", data: []byte("12345"), max: 4, code: agenterr.TooLarge},
		{name: "invalid utf8", path: "binary.txt", data: []byte{0xff}, code: agenterr.NotText},
		{name: "nul", path: "nul.txt", data: []byte("a\x00b"), code: agenterr.NotText},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			mustWrite(t, filepath.Join(root, test.path), test.data, 0o600)
			_, err := service.Read(ReadRequest{Root: "workspace", Path: test.path, MaxBytes: test.max})
			assertCode(t, err, test.code)
		})
	}

	_, err = service.Read(ReadRequest{Root: "workspace", Path: "."})
	assertCode(t, err, agenterr.NotFile)
	_, err = service.Read(ReadRequest{Root: "workspace", Path: "missing.txt"})
	assertCode(t, err, agenterr.NotFound)
	_, err = service.Read(ReadRequest{Root: "workspace", Path: "hello 世界.txt", MaxBytes: 33})
	assertCode(t, err, agenterr.InvalidInput)
}

func TestReadFileAllowsInternalSymlinksAndRejectsEscapes(t *testing.T) {
	service, root := newService(t, config.Readonly, 64, 64)
	mustWrite(t, filepath.Join(root, "target.txt"), []byte("inside"), 0o600)
	if err := os.Symlink("target.txt", filepath.Join(root, "inside-link")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	outside := filepath.Join(t.TempDir(), "outside.txt")
	mustWrite(t, outside, []byte("outside"), 0o600)
	if err := os.Symlink(outside, filepath.Join(root, "escape-link")); err != nil {
		t.Fatal(err)
	}

	result, err := service.Read(ReadRequest{Root: "workspace", Path: "inside-link"})
	if err != nil || result.Content != "inside" {
		t.Fatalf("internal symlink: result=%+v err=%v", result, err)
	}
	_, err = service.Read(ReadRequest{Root: "workspace", Path: "escape-link"})
	assertCode(t, err, agenterr.PathDenied)
}

func TestListDirectoryPagesAndFileInfoUsesLstat(t *testing.T) {
	service, root := newService(t, config.Readonly, 128, 128)
	mustWrite(t, filepath.Join(root, "alpha.txt"), []byte("abc"), 0o600)
	if err := os.Mkdir(filepath.Join(root, "beta"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("alpha.txt", filepath.Join(root, "gamma-link")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	first, err := service.List(ListRequest{Root: "workspace", Path: ".", Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Entries) != 2 || !first.HasMore || first.NextOffset == nil || *first.NextOffset != 2 {
		t.Fatalf("unexpected first page: %+v", first)
	}
	second, err := service.List(ListRequest{Root: "workspace", Path: ".", Offset: *first.NextOffset, Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Entries) != 1 || second.HasMore || second.NextOffset != nil {
		t.Fatalf("unexpected second page: %+v", second)
	}
	names := []string{first.Entries[0].Name, first.Entries[1].Name, second.Entries[0].Name}
	sort.Strings(names)
	if strings.Join(names, ",") != "alpha.txt,beta,gamma-link" {
		t.Fatalf("unexpected names: %v", names)
	}
	for _, entry := range append(first.Entries, second.Entries...) {
		if _, err := time.Parse(time.RFC3339Nano, entry.ModifiedAt); err != nil {
			t.Errorf("%s timestamp: %v", entry.Name, err)
		}
	}

	info, err := service.Info(InfoRequest{Root: "workspace", Path: "gamma-link"})
	if err != nil {
		t.Fatal(err)
	}
	if info.Name != "gamma-link" || info.Type != TypeSymlink || info.LinkTarget == nil || *info.LinkTarget != "alpha.txt" {
		t.Fatalf("unexpected link info: %+v", info)
	}
	_, err = service.List(ListRequest{Root: "workspace", Path: "alpha.txt"})
	assertCode(t, err, agenterr.NotDirectory)
	_, err = service.List(ListRequest{Root: "workspace", Path: ".", Offset: -1})
	assertCode(t, err, agenterr.InvalidInput)
	_, err = service.List(ListRequest{Root: "workspace", Path: ".", Limit: 201})
	assertCode(t, err, agenterr.InvalidInput)
}

func TestCreateDirectoryEnforcesModeAndParents(t *testing.T) {
	readonly, _ := newService(t, config.Readonly, 64, 64)
	_, err := readonly.CreateDirectory(CreateDirectoryRequest{Root: "workspace", Path: "new"})
	assertCode(t, err, agenterr.ModeDenied)

	service, root := newService(t, config.Workspace, 64, 64)
	_, err = service.CreateDirectory(CreateDirectoryRequest{Root: "workspace", Path: "."})
	assertCode(t, err, agenterr.InvalidInput)
	_, err = service.CreateDirectory(CreateDirectoryRequest{Root: "workspace", Path: "one/two"})
	assertCode(t, err, agenterr.NotFound)

	created, err := service.CreateDirectory(CreateDirectoryRequest{Root: "workspace", Path: "one/two", CreateParents: true})
	if err != nil || !created.Created {
		t.Fatalf("create parents: result=%+v err=%v", created, err)
	}
	again, err := service.CreateDirectory(CreateDirectoryRequest{Root: "workspace", Path: "one/two"})
	if err != nil || again.Created {
		t.Fatalf("repeat create: result=%+v err=%v", again, err)
	}
	mustWrite(t, filepath.Join(root, "file"), []byte("x"), 0o600)
	_, err = service.CreateDirectory(CreateDirectoryRequest{Root: "workspace", Path: "file"})
	assertCode(t, err, agenterr.NotDirectory)
}

func TestWriteFileIsAtomicNoClobberAndPreservesMode(t *testing.T) {
	service, root := newService(t, config.Workspace, 64, 64)
	created, err := service.Write(WriteRequest{
		Root: "workspace", Path: "space 世界/file.txt", Content: "first", CreateParents: true,
	})
	if err != nil || !created.Created || created.Bytes != 5 {
		t.Fatalf("create: result=%+v err=%v", created, err)
	}
	_, err = service.Write(WriteRequest{Root: "workspace", Path: "space 世界/file.txt", Content: "second"})
	assertCode(t, err, agenterr.AlreadyExists)
	data, err := os.ReadFile(filepath.Join(root, "space 世界", "file.txt"))
	if err != nil || string(data) != "first" {
		t.Fatalf("no-clobber changed file: %q, %v", data, err)
	}

	path := filepath.Join(root, "space 世界", "file.txt")
	if err := os.Chmod(path, 0o640); err != nil {
		t.Fatal(err)
	}
	replaced, err := service.Write(WriteRequest{Root: "workspace", Path: "space 世界/file.txt", Content: "second", Overwrite: true})
	if err != nil || replaced.Created {
		t.Fatalf("overwrite: result=%+v err=%v", replaced, err)
	}
	data, err = os.ReadFile(path)
	if err != nil || string(data) != "second" {
		t.Fatalf("overwrite content=%q err=%v", data, err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o640 {
		t.Fatalf("mode=%v", info.Mode().Perm())
	}
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), tempPrefix) {
			t.Errorf("temporary file leaked: %s", entry.Name())
		}
	}
}

func TestWriteFileRejectsInvalidContentTargetsAndSize(t *testing.T) {
	service, root := newService(t, config.Workspace, 64, 4)
	for _, test := range []struct {
		name    string
		content string
		code    agenterr.Code
	}{
		{name: "too large", content: "12345", code: agenterr.TooLarge},
		{name: "invalid utf8", content: string([]byte{0xff}), code: agenterr.NotText},
		{name: "nul", content: "a\x00b", code: agenterr.NotText},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := service.Write(WriteRequest{Root: "workspace", Path: test.name, Content: test.content})
			assertCode(t, err, test.code)
		})
	}
	if err := os.Mkdir(filepath.Join(root, "directory"), 0o700); err != nil {
		t.Fatal(err)
	}
	_, err := service.Write(WriteRequest{Root: "workspace", Path: "directory", Content: "x", Overwrite: true})
	assertCode(t, err, agenterr.NotFile)
	if err := os.Symlink("missing", filepath.Join(root, "link")); err == nil {
		_, err = service.Write(WriteRequest{Root: "workspace", Path: "link", Content: "x", Overwrite: true})
		assertCode(t, err, agenterr.NotFile)
	}
}

func TestWriteFileConcurrentNoClobberHasOneWinner(t *testing.T) {
	service, root := newService(t, config.Workspace, 64, 64)
	const writers = 8
	var wg sync.WaitGroup
	results := make(chan error, writers)
	for range writers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := service.Write(WriteRequest{Root: "workspace", Path: "winner.txt", Content: "same"})
			results <- err
		}()
	}
	wg.Wait()
	close(results)
	successes := 0
	for err := range results {
		if err == nil {
			successes++
		} else if agenterr.CodeOf(err) != agenterr.AlreadyExists {
			t.Errorf("unexpected error: %v", err)
		}
	}
	if successes != 1 {
		t.Fatalf("successes=%d, want 1", successes)
	}
	if data, err := os.ReadFile(filepath.Join(root, "winner.txt")); err != nil || string(data) != "same" {
		t.Fatalf("winner content=%q err=%v", data, err)
	}
}

func TestFileOperationsSharePublicationLock(t *testing.T) {
	service, _ := newService(t, config.Workspace, 64, 64)
	service.mu.Lock()
	writeDone := make(chan error, 1)
	listDone := make(chan error, 1)
	started := make(chan struct{}, 2)
	go func() {
		started <- struct{}{}
		_, err := service.Write(WriteRequest{Root: "workspace", Path: "file.txt", Content: "content"})
		writeDone <- err
	}()
	go func() {
		started <- struct{}{}
		_, err := service.List(ListRequest{Root: "workspace", Path: "."})
		listDone <- err
	}()
	<-started
	<-started
	time.Sleep(25 * time.Millisecond)
	bypassed := ""
	var bypassedErr error
	for name, done := range map[string]<-chan error{"write": writeDone, "list": listDone} {
		select {
		case err := <-done:
			bypassed, bypassedErr = name, err
		default:
		}
	}
	service.mu.Unlock()
	if bypassed != "" {
		t.Fatalf("%s bypassed publication lock: %v", bypassed, bypassedErr)
	}
	if err := <-writeDone; err != nil {
		t.Fatal(err)
	}
	if err := <-listDone; err != nil {
		t.Fatal(err)
	}
}

func TestEditFileRequiresExactlyOneMatch(t *testing.T) {
	service, root := newService(t, config.Workspace, 128, 128)
	mustWrite(t, filepath.Join(root, "edit.txt"), []byte("one two three"), 0o640)
	result, err := service.Edit(EditRequest{Root: "workspace", Path: "edit.txt", OldText: "two", NewText: "二"})
	if err != nil || result.BeforeSHA256 == result.AfterSHA256 || result.Bytes != len("one 二 three") {
		t.Fatalf("edit: result=%+v err=%v", result, err)
	}
	assertFile(t, filepath.Join(root, "edit.txt"), "one 二 three")

	for _, old := range []string{"absent", "e"} {
		before, _ := os.ReadFile(filepath.Join(root, "edit.txt"))
		_, err := service.Edit(EditRequest{Root: "workspace", Path: "edit.txt", OldText: old, NewText: "x"})
		assertCode(t, err, agenterr.InvalidInput)
		after, _ := os.ReadFile(filepath.Join(root, "edit.txt"))
		if string(after) != string(before) {
			t.Fatalf("failed edit %q changed content", old)
		}
	}
	_, err = service.Edit(EditRequest{Root: "workspace", Path: "edit.txt", OldText: "", NewText: "x"})
	assertCode(t, err, agenterr.InvalidInput)
	_, err = service.Edit(EditRequest{Root: "workspace", Path: "edit.txt", OldText: strings.Repeat("x", (512<<10)+1), NewText: "x"})
	assertCode(t, err, agenterr.TooLarge)
	_, err = service.Edit(EditRequest{Root: "workspace", Path: "edit.txt", OldText: "二", NewText: strings.Repeat("x", 129)})
	assertCode(t, err, agenterr.TooLarge)
	_, err = service.Edit(EditRequest{Root: "workspace", Path: "missing.txt", OldText: "x", NewText: "y"})
	assertCode(t, err, agenterr.NotFound)
	if err := os.Mkdir(filepath.Join(root, "directory"), 0o700); err != nil {
		t.Fatal(err)
	}
	_, err = service.Edit(EditRequest{Root: "workspace", Path: "directory", OldText: "x", NewText: "y"})
	assertCode(t, err, agenterr.NotFile)
}

func newService(t *testing.T, mode config.Mode, maxRead, maxWrite int) (*Service, string) {
	t.Helper()
	root := t.TempDir()
	configPath := filepath.Join(t.TempDir(), "config.json")
	document := map[string]any{
		"version": 1,
		"mode":    mode,
		"roots":   []map[string]string{{"name": "workspace", "path": root}},
		"limits":  map[string]int{"max_read_bytes": maxRead, "max_write_bytes": maxWrite},
	}
	data, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	mustWrite(t, configPath, data, 0o600)
	cfg, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	roots, err := policy.Open(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = roots.Close() })
	return New(roots, cfg.Limits()), root
}

func mustWrite(t *testing.T, path string, data []byte, mode os.FileMode) {
	t.Helper()
	if err := os.WriteFile(path, data, mode); err != nil {
		t.Fatal(err)
	}
}

func assertFile(t *testing.T, path, want string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil || string(data) != want {
		t.Fatalf("file=%q want=%q err=%v", data, want, err)
	}
}

func assertCode(t *testing.T, err error, want agenterr.Code) {
	t.Helper()
	if got := agenterr.CodeOf(err); got != want {
		t.Fatalf("error=%v code=%q want=%q", err, got, want)
	}
}
