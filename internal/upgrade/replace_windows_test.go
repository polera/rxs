//go:build windows

package upgrade

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestWindowsReplacementFailures(t *testing.T) {
	directErr := errors.New("direct replacement denied")
	moveErr := errors.New("move denied")
	installErr := errors.New("installation denied")
	restoreErr := errors.New("restoration denied")
	for _, test := range []struct {
		name        string
		failures    map[int]error
		cancelAfter int
		calls       int
		want        error
		wantRestore bool
		missing     bool
		updated     bool
	}{
		{name: "direct success", calls: 1, updated: true},
		{name: "fallback success", failures: map[int]error{1: directErr}, calls: 3, updated: true},
		{name: "move failure", failures: map[int]error{1: directErr, 2: moveErr}, calls: 2, want: moveErr},
		{name: "installation failure restored", failures: map[int]error{1: directErr, 3: installErr}, calls: 4, want: installErr},
		{name: "restoration failure", failures: map[int]error{1: directErr, 3: installErr, 4: restoreErr}, calls: 4, want: installErr, wantRestore: true, missing: true},
		{name: "cancel before moving", failures: map[int]error{1: directErr}, cancelAfter: 1, calls: 1, want: context.Canceled},
		{name: "cancel after moving restores", failures: map[int]error{1: directErr}, cancelAfter: 2, calls: 3, want: context.Canceled},
		{name: "cancel after moving restoration fails", failures: map[int]error{1: directErr, 3: restoreErr}, cancelAfter: 2, calls: 3, want: context.Canceled, wantRestore: true, missing: true},
		{name: "cancel does not interrupt rollback", failures: map[int]error{1: directErr, 3: installErr}, cancelAfter: 3, calls: 4, want: installErr},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "rxs.exe")
			if err := os.WriteFile(path, []byte("old"), 0o755); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			calls := 0
			err := replaceExecutableWithRename(ctx, path, []byte("new"), func(from, to string) error {
				calls++
				err := test.failures[calls]
				if err == nil {
					err = os.Rename(from, to)
				}
				if calls == test.cancelAfter {
					cancel()
				}
				return err
			})
			if calls != test.calls || !errors.Is(err, test.want) || errors.Is(err, restoreErr) != test.wantRestore {
				t.Fatalf("rename calls = %d; error = %v", calls, err)
			}
			if test.missing {
				if _, statErr := os.Stat(path); !errors.Is(statErr, os.ErrNotExist) {
					t.Fatalf("executable should be absent: %v", statErr)
				}
				if data, readErr := os.ReadFile(path + ".old"); readErr != nil || string(data) != "old" {
					t.Fatalf("recovery image = %q, %v", data, readErr)
				}
				if !strings.Contains(err.Error(), path+".old") || !strings.Contains(err.Error(), "manual recovery") {
					t.Fatalf("missing recovery instructions: %v", err)
				}
			} else {
				want := "old"
				if test.updated {
					want = "new"
				}
				if data, readErr := os.ReadFile(path); readErr != nil || string(data) != want {
					t.Fatalf("executable = %q, %v; want %q", data, readErr, want)
				}
				if _, statErr := os.Stat(path + ".old"); !errors.Is(statErr, os.ErrNotExist) {
					t.Fatalf("unexpected recovery image: %v", statErr)
				}
			}
			assertNoStagingFiles(t, filepath.Dir(path))
		})
	}
}

func TestWindowsExistingBackupIsPreserved(t *testing.T) {
	for _, directory := range []bool{false, true} {
		t.Run(fmt.Sprintf("directory=%v", directory), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "rxs.exe")
			if err := os.WriteFile(path, []byte("old"), 0o755); err != nil {
				t.Fatal(err)
			}
			backup := path + ".old"
			var err error
			if directory {
				err = os.Mkdir(backup, 0o755)
			} else {
				err = os.WriteFile(backup, []byte("previous recovery image"), 0o755)
			}
			if err != nil {
				t.Fatal(err)
			}
			err = replaceExecutableWithRename(context.Background(), path, []byte("new"), func(string, string) error {
				t.Fatal("rename must not run with an existing backup")
				return nil
			})
			if err == nil || !strings.Contains(err.Error(), backup) || !strings.Contains(err.Error(), "already exists") {
				t.Fatalf("backup error = %v", err)
			}
			if info, err := os.Stat(backup); err != nil || info.IsDir() != directory {
				t.Fatalf("backup changed: %v, %v", info, err)
			}
			if !directory {
				if data, err := os.ReadFile(backup); err != nil || string(data) != "previous recovery image" {
					t.Fatalf("backup = %q, %v", data, err)
				}
			}
			if data, err := os.ReadFile(path); err != nil || string(data) != "old" {
				t.Fatalf("executable = %q, %v", data, err)
			}
			assertNoStagingFiles(t, filepath.Dir(path))
		})
	}
}

func TestReplaceRunningExecutable(t *testing.T) {
	if os.Getenv("RXS_UPGRADE_RUNNING_HELPER") == "1" {
		fmt.Println("ready")
		_, _ = io.Copy(io.Discard, os.Stdin)
		os.Exit(0)
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	image, err := os.ReadFile(self)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "running.exe")
	if err := os.WriteFile(path, image, 0o755); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	child := exec.CommandContext(ctx, path, "-test.run=^TestReplaceRunningExecutable$")
	child.Env = append(os.Environ(), "RXS_UPGRADE_RUNNING_HELPER=1")
	stdin, err := child.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := child.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	child.Stderr = os.Stderr
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = stdin.Close()
		if err := child.Wait(); err != nil {
			t.Errorf("helper exit: %v", err)
		}
	}()
	if line, err := bufio.NewReader(stdout).ReadString('\n'); err != nil || strings.TrimSpace(line) != "ready" {
		t.Fatalf("helper readiness = %q, %v", line, err)
	}
	if err := replaceExecutable(ctx, path, []byte("new image")); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(path); err != nil || string(data) != "new image" {
		t.Fatalf("installed image = %q, %v", data, err)
	}
	assertNoStagingFiles(t, filepath.Dir(path))
}
