package filesave

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
)

func TestWriteProtectsExistingFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "download")
	if err := Write(path, []byte("original"), false); err != nil {
		t.Fatal(err)
	}
	if err := Write(path, []byte("replacement"), false); !errors.Is(err, fs.ErrExist) {
		t.Fatalf("expected exists error, got %v", err)
	}
	if b, err := os.ReadFile(path); err != nil || string(b) != "original" {
		t.Fatalf("original damaged: %q, %v", b, err)
	}
	if err := Write(path, []byte("replacement"), true); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(path); err != nil || string(b) != "replacement" {
		t.Fatalf("replacement: %q, %v", b, err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("temporary files left behind: %v, %v", entries, err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatalf("permissions: %o", info.Mode().Perm())
	}
}

func TestConcurrentNoOverwrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "download")
	var wg sync.WaitGroup
	results := make(chan error, 10)
	for i := range 10 {
		wg.Go(func() { results <- Write(path, []byte(fmt.Sprint(i)), false) })
	}
	wg.Wait()
	close(results)
	success := 0
	for err := range results {
		if err == nil {
			success++
		} else if !errors.Is(err, fs.ErrExist) {
			t.Fatal(err)
		}
	}
	if success != 1 {
		t.Fatalf("%d writes committed, want 1", success)
	}
}

func TestFailedCommitCleansTemporaryFile(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "directory")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, overwrite := range []bool{false, true} {
		if err := Write(target, []byte("data"), overwrite); err == nil {
			t.Fatal("replaced a directory")
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 || !entries[0].IsDir() {
		t.Fatalf("failed commit changed destination: %v, %v", entries, err)
	}
}
