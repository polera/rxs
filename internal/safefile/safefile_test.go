package safefile

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestWrite(t *testing.T) {
	for _, existing := range []bool{false, true} {
		name := "new"
		if existing {
			name = "existing"
		}
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "destination")
			wantMode := fs.FileMode(0o600)
			if existing {
				putFile(t, path, "old")
				if err := os.Chmod(path, 0o640); err != nil {
					t.Fatal(err)
				}
				info, err := os.Stat(path)
				if err != nil {
					t.Fatal(err)
				}
				wantMode = info.Mode().Perm()
			} else if runtime.GOOS == "windows" {
				wantMode = 0o666 // Windows maps writable files to these Go permission bits.
			}
			err := Write(path, 0o600, func(w io.Writer) error {
				if existing {
					assertContents(t, path, "old")
				} else if _, err := os.Stat(path); !errors.Is(err, fs.ErrNotExist) {
					t.Fatalf("destination visible before commit: %v", err)
				}
				_, err := io.WriteString(w, "complete replacement")
				return err
			})
			if err != nil {
				t.Fatal(err)
			}
			assertContents(t, path, "complete replacement")
			info, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			if info.Mode().Perm() != wantMode {
				t.Fatalf("mode = %o, want %o", info.Mode().Perm(), wantMode)
			}
			assertEntries(t, dir, "destination")
		})
	}
}

func TestWriteFailures(t *testing.T) {
	failure := errors.New("injected failure")
	for _, existing := range []bool{false, true} {
		for _, stage := range []string{"callback", "partial callback", "replace", "native replace"} {
			name := "new/" + stage
			if existing {
				name = "existing/" + stage
			}
			t.Run(name, func(t *testing.T) {
				dir := resolvedTempDir(t)
				path := filepath.Join(dir, "destination")
				if existing {
					putFile(t, path, "old")
				}
				var writer io.Writer
				replaceCalled := false
				err := writeFile(path, 0o600, func(w io.Writer) error {
					writer = w
					if stage == "callback" {
						return failure
					}
					if _, err := io.WriteString(w, "staged data"); err != nil {
						return err
					}
					if stage == "partial callback" {
						return failure
					}
					return nil
				}, func(source, destination string) error {
					replaceCalled = true
					if destination != path || filepath.Dir(source) != dir || source == path {
						t.Fatalf("replace(%q, %q) did not stage beside destination", source, destination)
					}
					assertContents(t, source, "staged data")
					if _, err := writer.Write([]byte("late write")); !errors.Is(err, fs.ErrClosed) {
						t.Fatalf("staged file not closed before replace: %v", err)
					}
					if stage == "native replace" {
						return replace(filepath.Join(dir, "missing-source"), destination)
					}
					return failure
				})
				if stage == "native replace" {
					if !errors.Is(err, fs.ErrNotExist) {
						t.Fatalf("error = %v, want missing source", err)
					}
				} else if !errors.Is(err, failure) {
					t.Fatalf("error = %v, want injected failure", err)
				}
				if want := stage == "replace" || stage == "native replace"; replaceCalled != want {
					t.Fatalf("replace called = %v, want %v", replaceCalled, want)
				}
				if _, err := writer.Write([]byte("late write")); !errors.Is(err, fs.ErrClosed) {
					t.Fatalf("staged file not closed after failure: %v", err)
				}
				if existing {
					assertContents(t, path, "old")
					assertEntries(t, dir, "destination")
				} else {
					assertEntries(t, dir)
				}
			})
		}
	}
}

func TestWriteSymlink(t *testing.T) {
	for _, kind := range []string{"relative", "absolute", "chain", "callback failure"} {
		t.Run(kind, func(t *testing.T) {
			dir := resolvedTempDir(t)
			targetDir := resolvedTempDir(t)
			target := filepath.Join(targetDir, "target")
			putFile(t, target, "old")
			link := filepath.Join(dir, "link")
			linkTarget := target
			if kind == "relative" {
				var err error
				linkTarget, err = filepath.Rel(dir, target)
				if err != nil {
					t.Fatal(err)
				}
			} else if kind == "chain" {
				linkTarget = filepath.Join(dir, "intermediate")
				symlink(t, target, linkTarget)
			}
			symlink(t, linkTarget, link)
			failure := errors.New("callback failure")
			err := writeFile(link, 0o600, func(w io.Writer) error {
				if _, err := io.WriteString(w, "new"); err != nil {
					return err
				}
				if kind == "callback failure" {
					return failure
				}
				return nil
			}, func(source, destination string) error {
				if filepath.Dir(source) != targetDir || destination != target {
					t.Fatalf("replace(%q, %q) did not resolve target", source, destination)
				}
				return replace(source, destination)
			})
			want := "new"
			if kind == "callback failure" {
				want = "old"
				if !errors.Is(err, failure) {
					t.Fatalf("error = %v, want callback failure", err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			assertContents(t, target, want)
			assertContents(t, link, want)
			if got, err := os.Readlink(link); err != nil || got != linkTarget {
				t.Fatalf("link changed: %q, %v", got, err)
			}
			assertEntries(t, targetDir, "target")
			if kind == "chain" {
				assertEntries(t, dir, "intermediate", "link")
			} else {
				assertEntries(t, dir, "link")
			}
		})
	}
}

func TestWriteRejectsInvalidDestinations(t *testing.T) {
	for _, kind := range []string{"directory", "directory link", "dangling link", "missing parent"} {
		t.Run(kind, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "destination")
			switch kind {
			case "directory":
				if err := os.Mkdir(path, 0o700); err != nil {
					t.Fatal(err)
				}
			case "directory link":
				symlink(t, dir, path)
			case "dangling link":
				symlink(t, "missing", path)
			case "missing parent":
				path = filepath.Join(path, "file")
			}
			if err := Write(path, 0o600, func(io.Writer) error {
				t.Fatal("callback called for invalid destination")
				return nil
			}); err == nil {
				t.Fatal("invalid destination accepted")
			}
			if kind == "missing parent" {
				assertEntries(t, dir)
			} else {
				assertEntries(t, dir, "destination")
				if kind != "directory" {
					if _, err := os.Readlink(path); err != nil {
						t.Fatalf("link replaced: %v", err)
					}
				}
			}
		})
	}
}

func TestWriteUsesNewFileMode(t *testing.T) {
	for _, mode := range []fs.FileMode{0o640, 0o400} {
		path := filepath.Join(t.TempDir(), "destination")
		if err := Write(path, mode, func(w io.Writer) error {
			_, err := io.WriteString(w, "new")
			return err
		}); err != nil {
			t.Fatal(err)
		}
		want := mode
		if runtime.GOOS == "windows" {
			want = 0o444
			if mode&0o200 != 0 {
				want = 0o666
			}
		}
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != want {
			t.Fatalf("new file mode for %o: %v, %v; want %o", mode, info, err, want)
		}
	}
}

func TestWriteThroughSymlinkedParent(t *testing.T) {
	dir := t.TempDir()
	link := filepath.Join(dir, "parent")
	target := t.TempDir()
	symlink(t, target, link)
	for _, contents := range []string{"created", "replaced"} {
		if err := Write(filepath.Join(link, "destination"), 0o600, func(w io.Writer) error {
			_, err := io.WriteString(w, contents)
			return err
		}); err != nil {
			t.Fatal(err)
		}
		assertContents(t, filepath.Join(target, "destination"), contents)
		assertEntries(t, target, "destination")
		if got, err := os.Readlink(link); err != nil || got != target {
			t.Fatalf("parent symlink changed: %q, %v", got, err)
		}
	}
}

func resolvedTempDir(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

func putFile(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
}

func assertContents(t *testing.T, path, want string) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil || string(got) != want {
		t.Fatalf("contents of %q = %q, %v; want %q", path, got, err, want)
	}
}

func assertEntries(t *testing.T, dir string, names ...string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != len(names) {
		t.Fatalf("directory %q contains %v, want %v", dir, entries, names)
	}
	for i, entry := range entries {
		if entry.Name() != names[i] {
			t.Fatalf("unexpected directory entry %q, want %q", entry.Name(), names[i])
		}
	}
}

func symlink(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		if runtime.GOOS == "windows" {
			t.Skipf("symlink creation unavailable: %v", err)
		}
		t.Fatal(err)
	}
}
