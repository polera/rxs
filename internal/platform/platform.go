package platform

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

const (
	BrowserSystem      = "system"
	BrowserTUI         = "tui"
	DefaultColorScheme = "default"
)

// Config contains machine-local behavior that should not be stored in the
// subscriptions database.
type Config struct {
	Browser    BrowserConfig    `json:"browser"`
	Appearance AppearanceConfig `json:"appearance"`
	Reading    ReadingConfig    `json:"reading"`
}

// AppearanceConfig contains visual settings for the terminal interface.
type AppearanceConfig struct {
	ColorScheme string `json:"color_scheme"`
}

// ReadingConfig controls how previewing and reading articles affects their
// read state.
type ReadingConfig struct {
	MarkReadOnScroll bool `json:"mark_read_on_scroll"`
	HideRead         bool `json:"hide_read"`
}

// BrowserConfig selects either the operating system browser or an interactive
// terminal browser. TUI Args may contain {url}; otherwise the URL is appended.
type BrowserConfig struct {
	Mode    string   `json:"mode"`
	Command string   `json:"command,omitempty"`
	Args    []string `json:"args,omitempty"`
}

func DefaultConfig() Config {
	return Config{
		Browser:    BrowserConfig{Mode: BrowserSystem},
		Appearance: AppearanceConfig{ColorScheme: DefaultColorScheme},
		Reading:    ReadingConfig{HideRead: true},
	}
}

// LoadConfig reads a JSON configuration file. A missing file is equivalent to
// the defaults so first-run startup never needs configuration.
func LoadConfig(path string) (Config, error) {
	config := DefaultConfig()
	file, err := os.Open(filepath.Clean(path))
	if errors.Is(err, os.ErrNotExist) {
		return config, nil
	}
	if err != nil {
		return Config{}, fmt.Errorf("open config: %w", err)
	}
	defer file.Close()
	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&config); err != nil {
		return Config{}, fmt.Errorf("parse config: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		if err == nil {
			err = errors.New("multiple JSON values")
		}
		return Config{}, fmt.Errorf("parse config: %w", err)
	}
	config.Browser.Mode = strings.ToLower(strings.TrimSpace(config.Browser.Mode))
	config.Appearance.ColorScheme = strings.ToLower(strings.TrimSpace(config.Appearance.ColorScheme))
	if config.Appearance.ColorScheme == "" {
		config.Appearance.ColorScheme = DefaultColorScheme
	}
	switch config.Browser.Mode {
	case BrowserSystem:
		return config, nil
	case BrowserTUI:
		if strings.TrimSpace(config.Browser.Command) == "" {
			return Config{}, errors.New("TUI browser command is required")
		}
		config.Browser.Command = strings.TrimSpace(config.Browser.Command)
		return config, nil
	default:
		return Config{}, fmt.Errorf("unknown browser mode %q (use %q or %q)", config.Browser.Mode, BrowserSystem, BrowserTUI)
	}
}

// SaveColorScheme updates the appearance setting while preserving the other
// values in the local configuration file. Missing files are created from the
// defaults.
func SaveColorScheme(path, name string) error {
	config, err := LoadConfig(path)
	if err != nil {
		return err
	}
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" {
		name = DefaultColorScheme
	}
	config.Appearance.ColorScheme = name
	return saveConfig(path, config)
}

func saveConfig(path string, config Config) error {
	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return fmt.Errorf("encode config: %w", err)
	}
	clean := filepath.Clean(path)
	if err := os.MkdirAll(filepath.Dir(clean), 0o700); err != nil {
		return fmt.Errorf("create config directory: %w", err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(clean, data, 0o600); err != nil {
		return fmt.Errorf("write config: %w", err)
	}
	return nil
}

func DataDir() (string, error) {
	if runtime.GOOS == "linux" {
		if value := os.Getenv("XDG_DATA_HOME"); value != "" {
			return filepath.Join(value, "rxs"), nil
		}
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("find user data directory: %w", err)
		}
		return filepath.Join(home, ".local", "share", "rxs"), nil
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("find user data directory: %w", err)
	}
	return filepath.Join(dir, "rxs"), nil
}

func ConfigDir() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("find config directory: %w", err)
	}
	return filepath.Join(dir, "rxs"), nil
}

func OpenBrowser(rawURL string) error {
	if err := validateBrowserURL(rawURL); err != nil {
		return err
	}
	var command *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		command = exec.Command("open", rawURL) // #nosec G204 -- rawURL is a validated HTTP(S) URL passed without a shell.
	case "windows":
		command = exec.Command("rundll32", "url.dll,FileProtocolHandler", rawURL) // #nosec G204 -- rawURL is a validated HTTP(S) URL passed without a shell.
	default:
		command = exec.Command("xdg-open", rawURL) // #nosec G204 -- rawURL is a validated HTTP(S) URL passed without a shell.
	}
	if err := command.Start(); err != nil {
		return fmt.Errorf("open browser: %w", err)
	}
	return nil
}

// ValidateBrowserConfig checks whether a configured interactive browser can be
// launched. Callers can surface the result as a warning without preventing the
// rest of the application from starting.
func ValidateBrowserConfig(config BrowserConfig) error {
	if config.Mode != BrowserTUI {
		return nil
	}
	command := strings.TrimSpace(config.Command)
	if command == "" {
		return errors.New("TUI browser command is required")
	}
	if _, err := exec.LookPath(command); err != nil {
		return fmt.Errorf("browser command %q was not found; update browser.command in config", command)
	}
	return nil
}

// BrowserCommand builds an interactive command for use with Bubble Tea's
// terminal-releasing ExecProcess command.
func BrowserCommand(config BrowserConfig, rawURL string) (*exec.Cmd, error) {
	if err := validateBrowserURL(rawURL); err != nil {
		return nil, err
	}
	if strings.TrimSpace(config.Command) == "" {
		return nil, errors.New("TUI browser command is required")
	}
	args := append([]string(nil), config.Args...)
	foundPlaceholder := false
	for index := range args {
		if strings.Contains(args[index], "{url}") {
			args[index] = strings.ReplaceAll(args[index], "{url}", rawURL)
			foundPlaceholder = true
		}
	}
	if !foundPlaceholder {
		args = append(args, rawURL)
	}
	// The executable and arguments are explicitly supplied in the user's local
	// configuration. exec.Command executes them directly without a shell.
	return exec.Command(config.Command, args...), nil // #nosec G204
}

func validateBrowserURL(rawURL string) error {
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return errors.New("link has no valid http or https URL")
	}
	return nil
}
