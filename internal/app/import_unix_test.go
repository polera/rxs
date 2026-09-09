//go:build unix

package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"golang.org/x/sys/unix"
)

func TestImportRejectsFIFOWithoutOpeningOrReading(t *testing.T) {
	for _, writer := range []bool{false, true} {
		t.Run(map[bool]string{false: "no writer", true: "stalled writer"}[writer], func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "feeds.opml")
			if err := unix.Mkfifo(path, 0o600); err != nil {
				t.Fatal(err)
			}
			if writer {
				// Keep a writer connected without supplying any data. An ordinary
				// open would succeed here, but parsing would block waiting to read.
				file, err := os.OpenFile(path, os.O_RDWR|syscall.O_NONBLOCK, 0)
				if err != nil {
					t.Fatal(err)
				}
				defer file.Close()
			}
			model, store := loadedModel(t)
			cmd := model.importCmd(path)
			done := make(chan importMsg, 1)
			go func() { done <- cmd().(importMsg) }()
			msg := receive(t, done)
			if msg.err == nil || !strings.Contains(msg.err.Error(), "regular file") || len(store.addURLs) != 0 {
				t.Fatalf("FIFO import = %#v", msg)
			}
			shutdown := make(chan error, 1)
			go func() { shutdown <- model.Shutdown(context.Background()) }()
			if err := receive(t, shutdown); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestImportOpenFlagsProtectRegularToFIFOSubstitution(t *testing.T) {
	path := filepath.Join(t.TempDir(), "feeds.opml")
	if err := os.WriteFile(path, []byte(importFixture), 0o600); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := unix.Mkfifo(path, 0o600); err != nil {
		t.Fatal(err)
	}
	// Reproduce the Stat/OpenFile window deterministically, using the exact
	// production flags. Open must return so descriptor validation can reject it.
	done := make(chan error, 1)
	go func() {
		file, err := os.OpenFile(path, importOpenFlags, 0)
		if err != nil {
			done <- err
			return
		}
		defer file.Close()
		after, err := file.Stat()
		if err == nil && after.Mode().IsRegular() && os.SameFile(before, after) {
			t.Error("replacement would pass regular-file identity validation")
		}
		done <- err
	}()
	if err := receive(t, done); err != nil {
		t.Fatal(err)
	}
}
