// Package safefile stages complete files before replacing their destinations.
package safefile

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

// Write calls write with a temporary file beside path, then syncs, closes, and
// replaces path only after all these steps succeed. The caller must check its
// writes and flush any buffers before returning from write. Parent directories
// must already exist. Existing regular-file permissions are preserved; mode is
// used for new files. Ownership, ACLs, and other metadata are not copied.
// On Windows, mode controls only the read-only attribute; access is governed by
// inherited ACLs, not Unix permission bits.
// Existing symlinks are resolved and their targets replaced, leaving the links
// intact. Dangling symlinks and nonregular destinations are rejected.
//
// Replacement uses rename on Unix (atomic visibility on supporting filesystems)
// and MoveFileEx with REPLACE_EXISTING on Windows, without a delete-first or copy
// fallback. Windows and other OS/filesystem combinations do not have a universal
// atomic-visibility guarantee. File contents are synced, but the parent directory
// is not: success does not guarantee the replacement survives a crash. Callers
// must coordinate concurrent writes and path changes; this is not a defense
// against concurrent, hostile directory modification.
func Write(path string, mode fs.FileMode, write func(io.Writer) error) error {
	return writeFile(path, mode, write, replace)
}

func writeFile(path string, mode fs.FileMode, write func(io.Writer) error, replace func(string, string) error) (err error) {
	path, err = filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("resolve destination: %w", err)
	}
	_, err = os.Lstat(path)
	if err == nil {
		path, err = filepath.EvalSymlinks(path)
		if err != nil {
			return fmt.Errorf("resolve destination: %w", err)
		}
		info, err := os.Stat(path)
		if err != nil {
			return fmt.Errorf("stat destination: %w", err)
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("destination %q is not a regular file", path)
		}
		mode = info.Mode()
	} else if !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("stat destination: %w", err)
	}

	file, err := os.CreateTemp(filepath.Dir(path), ".rxs-*")
	if err != nil {
		return fmt.Errorf("create staged file: %w", err)
	}
	closed, committed := false, false
	defer func() {
		if !closed {
			if closeErr := file.Close(); closeErr != nil {
				err = errors.Join(err, fmt.Errorf("close staged file: %w", closeErr))
			}
		}
		if !committed {
			if removeErr := os.Remove(file.Name()); removeErr != nil && !errors.Is(removeErr, fs.ErrNotExist) {
				err = errors.Join(err, fmt.Errorf("remove staged file: %w", removeErr))
			}
		}
	}()
	if err := write(file); err != nil {
		return fmt.Errorf("write staged file: %w", err)
	}
	if err := file.Chmod(mode & (fs.ModePerm | fs.ModeSetuid | fs.ModeSetgid | fs.ModeSticky)); err != nil {
		return fmt.Errorf("chmod staged file: %w", err)
	}
	if err := file.Sync(); err != nil {
		return fmt.Errorf("sync staged file: %w", err)
	}
	closed = true
	if err := file.Close(); err != nil {
		return fmt.Errorf("close staged file: %w", err)
	}
	if err := replace(file.Name(), path); err != nil {
		return fmt.Errorf("replace destination: %w", err)
	}
	committed = true
	return nil
}
