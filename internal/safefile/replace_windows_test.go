package safefile

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"
)

func TestWriteWindowsSharingViolationPreservesDestination(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "destination")
	putFile(t, path, "old")
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		t.Fatal(err)
	}
	// Deny FILE_SHARE_DELETE so the native replacement must fail.
	handle, err := windows.CreateFile(name, windows.GENERIC_READ,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil,
		windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(handle)
	err = Write(path, 0o600, func(w io.Writer) error {
		_, err := io.WriteString(w, "new")
		return err
	})
	if !errors.Is(err, windows.ERROR_SHARING_VIOLATION) {
		t.Fatalf("error = %v, want sharing violation", err)
	}
	assertContents(t, path, "old")
	assertEntries(t, dir, "destination")
}

func TestWriteWindowsReadOnlyFailureCleansStaging(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "destination")
	putFile(t, path, "old")
	if err := os.Chmod(path, 0o400); err != nil {
		t.Fatal(err)
	}
	err := Write(path, 0o600, func(w io.Writer) error {
		_, err := io.WriteString(w, "new")
		return err
	})
	if !errors.Is(err, windows.ERROR_ACCESS_DENIED) {
		t.Fatalf("error = %v, want access denied", err)
	}
	assertContents(t, path, "old")
	assertEntries(t, dir, "destination")
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o444 {
		t.Fatalf("read-only permissions changed: %v, %v", info, err)
	}
}
