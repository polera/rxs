package platform

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
)

func TestDataDirUsesXDGDataHome(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("XDG data path is Linux-specific")
	}
	base := t.TempDir()
	t.Setenv("XDG_DATA_HOME", base)
	dir, err := DataDir()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(base, "rxs"); dir != want {
		t.Fatalf("DataDir() = %q, want %q", dir, want)
	}
}

func TestOpenBrowserRejectsNonHTTPURL(t *testing.T) {
	if err := OpenBrowser("file:///tmp/article"); err == nil {
		t.Fatal("expected non-HTTP URL to be rejected")
	}
}

func TestLoadConfigDefaultsWhenMissing(t *testing.T) {
	config, err := LoadConfig(filepath.Join(t.TempDir(), "missing.json"))
	if err != nil {
		t.Fatal(err)
	}
	if config.Browser.Mode != BrowserSystem {
		t.Fatalf("browser mode = %q, want %q", config.Browser.Mode, BrowserSystem)
	}
}

func TestLoadTUIBrowserConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{
		"browser": {"mode": "tui", "command": "w3m", "args": ["-M", "{url}"]}
	}`), 0o600); err != nil {
		t.Fatal(err)
	}
	config, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if config.Browser.Mode != BrowserTUI || config.Browser.Command != "w3m" {
		t.Fatalf("browser config = %#v", config.Browser)
	}
	command, err := BrowserCommand(config.Browser, "https://example.test/article")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"w3m", "-M", "https://example.test/article"}
	if !reflect.DeepEqual(command.Args, want) {
		t.Fatalf("command args = %#v, want %#v", command.Args, want)
	}
}

func TestLoadConfigRejectsTUIWithoutCommand(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"browser":{"mode":"tui"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadConfig(path); err == nil {
		t.Fatal("expected missing TUI browser command to be rejected")
	}
}

func TestBrowserCommandAppendsURLWithoutPlaceholder(t *testing.T) {
	command, err := BrowserCommand(BrowserConfig{Command: "lynx", Args: []string{"-accept_all_cookies"}}, "https://example.test")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"lynx", "-accept_all_cookies", "https://example.test"}
	if !reflect.DeepEqual(command.Args, want) {
		t.Fatalf("command args = %#v, want %#v", command.Args, want)
	}
}
