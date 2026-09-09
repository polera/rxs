package upgrade

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestWriteReplacementCancellation(t *testing.T) {
	for _, after := range []int{1, 64 << 10} {
		path := filepath.Join(t.TempDir(), "rxs")
		if err := os.WriteFile(path, []byte("old"), 0o751); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		source := &cancelReader{reader: bytes.NewReader(make([]byte, 64<<10)), after: after, cancel: cancel}
		temp, err := writeReplacement(ctx, path, source)
		cancel()
		if !errors.Is(err, context.Canceled) || temp != "" {
			t.Fatalf("staging = %q, %v", temp, err)
		}
		if data, err := os.ReadFile(path); err != nil || string(data) != "old" {
			t.Fatalf("original = %q, %v", data, err)
		}
		assertNoStagingFiles(t, filepath.Dir(path))
	}
}

func TestReplaceExecutableCanceled(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rxs")
	if err := os.WriteFile(path, []byte("old"), 0o751); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := replaceExecutable(ctx, path, []byte("new")); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(path); err != nil || string(data) != "old" {
		t.Fatalf("original = %q, %v", data, err)
	}
	assertNoStagingFiles(t, filepath.Dir(path))
}

func TestWriteReplacementPreservesPermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rxs")
	if err := os.WriteFile(path, []byte("old"), 0o751); err != nil {
		t.Fatal(err)
	}
	temp, err := writeReplacement(context.Background(), path, bytes.NewReader([]byte("new")))
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(temp)
	if data, err := os.ReadFile(temp); err != nil || string(data) != "new" {
		t.Fatalf("staged contents = %q, %v", data, err)
	}
	info, err := os.Stat(temp)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o751 {
		t.Fatalf("mode = %v", info.Mode())
	}
}

func assertNoStagingFiles(t *testing.T, dir string) {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(dir, ".rxs-upgrade-*"))
	if err != nil || len(files) != 0 {
		t.Fatalf("staging files = %v, %v", files, err)
	}
}
