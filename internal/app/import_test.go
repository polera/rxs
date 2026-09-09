package app

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/polera/rxs/internal/opml"
)

const importFixture = `<opml version="2.0"><body><outline xmlUrl="https://one.test/rss"/><outline xmlUrl="https://two.test/rss"/></body></opml>`

func TestImportRequiresRegularFile(t *testing.T) {
	for _, source := range []string{"regular", "symlink", "directory"} {
		t.Run(source, func(t *testing.T) {
			model, store := loadedModel(t)
			path := t.TempDir()
			if source != "directory" {
				path = filepath.Join(path, "feeds.opml")
				if err := os.WriteFile(path, []byte(importFixture), 0o600); err != nil {
					t.Fatal(err)
				}
				if source == "symlink" {
					link := filepath.Join(filepath.Dir(path), "link.opml")
					if err := os.Symlink(path, link); err != nil {
						t.Skipf("symlinks unavailable: %v", err)
					}
					path = link
				}
			}
			msg := model.importCmd(path)().(importMsg)
			if source == "directory" {
				if msg.err == nil || !strings.Contains(msg.err.Error(), "regular file") || len(store.addURLs) != 0 {
					t.Fatalf("nonregular import = %#v", msg)
				}
			} else if msg.err != nil || msg.count != 2 || len(store.addURLs) != 2 {
				t.Fatalf("regular import = %#v, additions = %v", msg, store.addURLs)
			}
			if err := model.Shutdown(context.Background()); err != nil {
				t.Fatal(err)
			}
		})
	}
}

type cancelingImportReader struct {
	reader io.Reader
	cancel context.CancelFunc
	calls  int
}

func (r *cancelingImportReader) Read(buffer []byte) (int, error) {
	r.calls++
	n, err := r.reader.Read(buffer)
	r.cancel()
	return n, err
}

func TestImportReaderChecksCancellationBeforeAndAfterRead(t *testing.T) {
	file, err := os.CreateTemp(t.TempDir(), "feeds-*.opml")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if _, err := file.WriteString(importFixture); err != nil {
		t.Fatal(err)
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	underlying := &cancelingImportReader{reader: file, cancel: cancel}
	reader := importReader{ctx: ctx, reader: underlying}
	if subscriptions, err := opml.Import(reader); !errors.Is(err, context.Canceled) || len(subscriptions) != 0 {
		t.Fatalf("cancellation at read boundary: subscriptions=%v err=%v", subscriptions, err)
	}
	if n, err := reader.Read(make([]byte, 10)); n != 0 || !errors.Is(err, context.Canceled) || underlying.calls != 1 {
		t.Fatalf("canceled reader touched source: n=%d err=%v calls=%d", n, err, underlying.calls)
	}
	if file, err := openImportFile(ctx, "missing"); file != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled open = %v, %v", file, err)
	}
}

func TestImportReaderBoundsReadChunks(t *testing.T) {
	reader := importReader{ctx: context.Background(), reader: strings.NewReader(strings.Repeat("x", 64<<10))}
	if n, err := reader.Read(make([]byte, 64<<10)); err != nil || n != 32<<10 {
		t.Fatalf("read chunk = %d, %v", n, err)
	}
}
