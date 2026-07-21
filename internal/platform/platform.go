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
	BrowserSystem = "system"
	BrowserTUI    = "tui"
)

// Config contains machine-local behavior that should not be stored in the
// subscriptions database.
type Config struct {
	Browser BrowserConfig `json:"browser"`
}

// BrowserConfig selects either the operating system browser or an interactive
// terminal browser. TUI Args may contain {url}; otherwise the URL is appended.
type BrowserConfig struct {
	Mode    string   `json:"mode"`
	Command string   `json:"command,omitempty"`
	Args    []string `json:"args,omitempty"`
}

func DefaultConfig() Config {
	return Config{Browser: BrowserConfig{Mode: BrowserSystem}}
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
	var command string
	var args []string
	switch runtime.GOOS {
	case "darwin":
		command, args = "open", []string{rawURL}
	case "windows":
		command, args = "rundll32", []string{"url.dll,FileProtocolHandler", rawURL}
	default:
		command, args = "xdg-open", []string{rawURL}
	}
	if err := exec.Command(command, args...).Start(); err != nil {
		return fmt.Errorf("open browser: %w", err)
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
	return exec.Command(config.Command, args...), nil
}

func validateBrowserURL(rawURL string) error {
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return errors.New("link has no valid http or https URL")
	}
	return nil
}
