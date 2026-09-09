package platform

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
	"time"
)

func TestDataDirUsesXDGDataHome(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "freebsd" {
		t.Skip("XDG data path is Linux- and FreeBSD-specific")
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
	if config.Content.FullArticles != FullArticlesOff {
		t.Fatalf("full_articles = %q, want %q", config.Content.FullArticles, FullArticlesOff)
	}
}

func TestLoadFullArticleConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"content":{"full_articles":"  AuTo  "}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	config, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if config.Content.FullArticles != FullArticlesAuto {
		t.Fatalf("full_articles = %q, want %q", config.Content.FullArticles, FullArticlesAuto)
	}
}

func TestLoadConfigRejectsUnknownFullArticleMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"content":{"full_articles":"always"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadConfig(path); err == nil {
		t.Fatal("unsupported full_articles mode was accepted")
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

func TestSaveColorSchemePreservesFilePolicyAndSettings(t *testing.T) {
	for _, link := range []bool{false, true} {
		name := "regular"
		if link {
			name = "symlink"
		}
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			target := filepath.Join(dir, "config.json")
			original := Config{
				Browser:    BrowserConfig{Mode: BrowserTUI, Command: "w3m", Args: []string{"-M", "{url}"}},
				Appearance: AppearanceConfig{ColorScheme: "dracula"},
				Reading:    ReadingConfig{MarkReadOnScroll: true, HideRead: false},
				Content:    ContentConfig{FullArticles: FullArticlesAuto},
			}
			if err := saveConfig(target, original); err != nil {
				t.Fatal(err)
			}
			if info, err := os.Stat(target); err != nil {
				t.Fatal(err)
			} else if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
				t.Fatalf("new config mode = %o, want 600", info.Mode().Perm())
			}
			if err := os.Chmod(target, 0o640); err != nil {
				t.Fatal(err)
			}
			before, err := os.Stat(target)
			if err != nil {
				t.Fatal(err)
			}
			path := target
			if link {
				path = filepath.Join(t.TempDir(), "link.json")
				if err := os.Symlink(target, path); err != nil {
					if runtime.GOOS == "windows" {
						t.Skipf("symlink creation unavailable: %v", err)
					}
					t.Fatal(err)
				}
			}
			if err := SaveColorScheme(path, "  NoRd  "); err != nil {
				t.Fatal(err)
			}
			got, err := LoadConfig(target)
			if err != nil {
				t.Fatal(err)
			}
			original.Appearance.ColorScheme = "nord"
			if !reflect.DeepEqual(got, original) {
				t.Fatalf("saved config = %#v, want %#v", got, original)
			}
			after, err := os.Stat(target)
			if err != nil || after.Mode().Perm() != before.Mode().Perm() {
				t.Fatalf("config permissions changed: %v, %v", after, err)
			}
			if link {
				if got, err := os.Readlink(path); err != nil || got != target {
					t.Fatalf("config symlink changed: %q, %v", got, err)
				}
			}
			if entries, err := os.ReadDir(dir); err != nil || len(entries) != 1 {
				t.Fatalf("staging files left behind: %v, %v", entries, err)
			}
		})
	}
}

func TestSaveColorSchemeLeavesInvalidConfigUntouched(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	const original = `{"browser": {"mode": "tui"}, "unknown": true}`
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := SaveColorScheme(path, "nord"); err == nil {
		t.Fatal("invalid config was overwritten")
	}
	if got, err := os.ReadFile(path); err != nil || string(got) != original {
		t.Fatalf("config changed: %q, %v", got, err)
	}
	if entries, err := os.ReadDir(dir); err != nil || len(entries) != 1 {
		t.Fatalf("staging files left behind: %v, %v", entries, err)
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

func TestSaveColorSchemePreservesContentConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{
		"content": {"full_articles": "auto"},
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
	if config.Content.FullArticles != FullArticlesAuto {
		t.Fatalf("saving the color scheme discarded content configuration: %#v", config.Content)
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
	commandPath, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", filepath.Dir(commandPath))
	if err := ValidateBrowserConfig(BrowserConfig{Mode: BrowserTUI, Command: filepath.Base(commandPath)}); err != nil {
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
	if command.Process != nil || command.ProcessState != nil {
		t.Fatal("BrowserCommand started a process owned by Bubble Tea")
	}
}

func TestStartBrowserLifecycle(t *testing.T) {
	for _, result := range []string{"success", "failure"} {
		t.Run(result, func(t *testing.T) {
			executable, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			// The deadline is only a deadlock guard; pipe handshakes control ordering.
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, executable, "-test.run=^TestBrowserHelperProcess$")
			command.Env = append(os.Environ(), "RXS_TEST_BROWSER_HELPER="+result)
			input, err := command.StdinPipe()
			if err != nil {
				t.Fatal(err)
			}
			defer input.Close()
			output, err := command.StdoutPipe()
			if err != nil {
				t.Fatal(err)
			}
			defer output.Close()
			done, err := startBrowser(command)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				cancel()
				select {
				case <-done:
				case <-time.After(10 * time.Second):
					t.Error("launcher was not reaped during cleanup")
				}
			}()
			var ready [1]byte
			if _, err := io.ReadFull(output, ready[:]); err != nil || ready[0] != 'R' {
				t.Fatalf("helper readiness = %q, %v", ready, err)
			}
			select {
			case err := <-done:
				t.Fatalf("launcher completed before release: %v", err)
			default:
			}
			if _, err := input.Write([]byte("X")); err != nil {
				t.Fatal(err)
			}
			select {
			case err := <-done:
				if result == "failure" {
					var exitErr *exec.ExitError
					if !errors.As(err, &exitErr) || exitErr.ExitCode() != 7 {
						t.Fatalf("Wait error = %v, want exit code 7", err)
					}
				} else if err != nil {
					t.Fatal(err)
				}
			case <-ctx.Done():
				t.Fatal("launcher was not reaped after release")
			}
			if command.ProcessState == nil || !command.ProcessState.Exited() {
				t.Fatal("launcher has no completed process state")
			}
			if _, open := <-done; open {
				t.Fatal("completion channel was not closed")
			}
		})
	}
}

func TestStartBrowserReturnsStartError(t *testing.T) {
	command := exec.Command(filepath.Join(t.TempDir(), "missing-browser"))
	done, err := startBrowser(command)
	if !errors.Is(err, os.ErrNotExist) || done != nil || command.Process != nil {
		t.Fatalf("start = (%v, %v), process = %v", done, err, command.Process)
	}
}

func TestBrowserHelperProcess(t *testing.T) {
	result := os.Getenv("RXS_TEST_BROWSER_HELPER")
	if result == "" {
		return
	}
	if _, err := os.Stdout.Write([]byte("R")); err != nil {
		os.Exit(2)
	}
	var release [1]byte
	if _, err := io.ReadFull(os.Stdin, release[:]); err != nil || release[0] != 'X' {
		os.Exit(3)
	}
	if result == "failure" {
		os.Exit(7)
	}
	os.Exit(0)
}
