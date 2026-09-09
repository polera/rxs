//go:build unix

package safefile

import (
	"io"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func TestWriteRejectsFIFO(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "fifo")
	if err := unix.Mkfifo(path, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Write(path, 0o600, func(io.Writer) error {
		t.Fatal("callback called for FIFO")
		return nil
	}); err == nil {
		t.Fatal("FIFO accepted")
	}
	info, err := os.Lstat(path)
	if err != nil || info.Mode()&os.ModeNamedPipe == 0 {
		t.Fatalf("FIFO replaced: %v, %v", info, err)
	}
	assertEntries(t, dir, "fifo")
}
