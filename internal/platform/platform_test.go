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
	if config.Appearance.ColorScheme != DefaultColorScheme {
		t.Fatalf("color scheme = %q, want %q", config.Appearance.ColorScheme, DefaultColorScheme)
	}
	if config.Reading.MarkReadOnScroll {
		t.Fatal("mark_read_on_scroll defaulted to true")
	}
	if !config.Reading.HideRead {
		t.Fatal("hide_read did not default to true")
	}
}

func TestLoadReadingConfigEnablesMarkReadOnScroll(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"reading":{"mark_read_on_scroll":true}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	config, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if !config.Reading.MarkReadOnScroll {
		t.Fatal("mark_read_on_scroll was not enabled")
	}
	if !config.Reading.HideRead {
		t.Fatal("omitting hide_read changed its true default")
	}
}

func TestLoadReadingConfigCanShowReadArticles(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"reading":{"hide_read":false}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	config, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if config.Reading.HideRead {
		t.Fatal("hide_read=false was not applied")
	}
}

func TestLoadConfigRejectsUnknownReadingField(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"reading":{"mark_on_open":true}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadConfig(path); err == nil {
		t.Fatal("unknown reading field was accepted")
	}
}

func TestLoadAppearanceConfigNormalizesColorScheme(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"appearance":{"color_scheme":"  SoLaRiZeD-LiGhT  "}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	config, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if config.Appearance.ColorScheme != "solarized-light" {
		t.Fatalf("color scheme = %q, want %q", config.Appearance.ColorScheme, "solarized-light")
	}
}

func TestSaveColorSchemeCreatesConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "config.json")
	if err := SaveColorScheme(path, "  NoRd  "); err != nil {
		t.Fatal(err)
	}
	config, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if config.Appearance.ColorScheme != "nord" {
		t.Fatalf("color scheme = %q, want %q", config.Appearance.ColorScheme, "nord")
	}
	if config.Browser.Mode != BrowserSystem {
		t.Fatalf("browser mode = %q, want %q", config.Browser.Mode, BrowserSystem)
	}
}

func TestSaveColorSchemePreservesBrowserConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{
		"browser": {"mode": "tui", "command": "w3m", "args": ["-M", "{url}"]},
		"appearance": {"color_scheme": "dracula"}
	}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := SaveColorScheme(path, "solarized-light"); err != nil {
		t.Fatal(err)
	}
	config, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if config.Appearance.ColorScheme != "solarized-light" {
		t.Fatalf("color scheme = %q", config.Appearance.ColorScheme)
	}
	if config.Browser.Mode != BrowserTUI || config.Browser.Command != "w3m" ||
		!reflect.DeepEqual(config.Browser.Args, []string{"-M", "{url}"}) {
		t.Fatalf("browser config changed: %#v", config.Browser)
	}
}

func TestSaveColorSchemePreservesReadingConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{
		"reading": {"mark_read_on_scroll": true, "hide_read": false},
		"appearance": {"color_scheme": "dracula"}
	}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := SaveColorScheme(path, "nord"); err != nil {
		t.Fatal(err)
	}
	config, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if !config.Reading.MarkReadOnScroll {
		t.Fatal("saving the color scheme discarded reading configuration")
	}
	if config.Reading.HideRead {
		t.Fatal("saving the color scheme discarded hide_read=false")
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

func TestValidateBrowserConfigWarnsWhenCommandDoesNotExist(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	err := ValidateBrowserConfig(BrowserConfig{Mode: BrowserTUI, Command: "missing-browser"})
	if err == nil || err.Error() != `browser command "missing-browser" was not found; update browser.command in config` {
		t.Fatalf("error = %v", err)
	}
}

func TestValidateBrowserConfigAcceptsCommandOnPath(t *testing.T) {
	dir := t.TempDir()
	commandPath := filepath.Join(dir, "test-browser")
	if err := os.WriteFile(commandPath, []byte("#!/bin/sh\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	if err := ValidateBrowserConfig(BrowserConfig{Mode: BrowserTUI, Command: "test-browser"}); err != nil {
		t.Fatal(err)
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
