package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"

	tea "charm.land/bubbletea/v2"
	"github.com/polera/rxs/internal/app"
	feedservice "github.com/polera/rxs/internal/feed"
	"github.com/polera/rxs/internal/platform"
	"github.com/polera/rxs/internal/store"
	"github.com/polera/rxs/internal/ui"
)

var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "rxs:", err)
		os.Exit(1)
	}
}

func run() error {
	return runArgs(os.Args[1:], os.Stdout, os.Stderr)
}

func runArgs(args []string, stdout, stderr io.Writer) error {
	defaultPath, err := databasePath()
	if err != nil {
		return err
	}
	configDir, err := platform.ConfigDir()
	if err != nil {
		return err
	}
	flags := flag.NewFlagSet("rxs", flag.ContinueOnError)
	flags.SetOutput(stderr)
	dbPath := flags.String("db", defaultPath, "SQLite database path")
	configPath := flags.String("config", filepath.Join(configDir, "config.json"), "local configuration path")
	showVersion := flags.Bool("version", false, "print version and exit")
	flags.Usage = func() {
		fmt.Fprintln(stderr, "Usage: rxs [options]")
		fmt.Fprintln(stderr, "       rxs [options] add URL")
		fmt.Fprintln(stderr, "\nOptions:")
		flags.PrintDefaults()
	}
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if *showVersion {
		fmt.Fprintf(stdout, "rxs %s (%s, %s)\n", version, commit, date)
		return nil
	}
	commandArgs := flags.Args()
	if len(commandArgs) > 0 {
		switch commandArgs[0] {
		case "add":
			return runAddCommand(context.Background(), commandArgs[1:], *dbPath, stdout, stderr)
		default:
			return fmt.Errorf("unknown command %q", commandArgs[0])
		}
	}
	config, err := platform.LoadConfig(*configPath)
	if err != nil {
		return err
	}
	styles, err := ui.ResolveScheme(config.Appearance.ColorScheme)
	if err != nil {
		return err
	}
	repository, err := openStore(*dbPath)
	if err != nil {
		return err
	}
	defer repository.Close()
	client := feedservice.NewClient(version)
	refresher := feedservice.NewService(repository, client)
	var model app.Model
	if config.Browser.Mode == platform.BrowserTUI {
		model = app.NewWithTUIBrowser(repository, refresher, func(rawURL string) (*exec.Cmd, error) {
			return platform.BrowserCommand(config.Browser, rawURL)
		}, styles)
		if err := platform.ValidateBrowserConfig(config.Browser); err != nil {
			model.SetWarningStatus(err.Error())
		}
	} else {
		model = app.New(repository, refresher, platform.OpenBrowser, styles)
	}
	model.SetColorSchemeSaver(func(name string) error {
		return platform.SaveColorScheme(*configPath, name)
	})
	_, err = tea.NewProgram(model).Run()
	return err
}

func runAddCommand(ctx context.Context, args []string, defaultDBPath string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("rxs add", flag.ContinueOnError)
	flags.SetOutput(stderr)
	dbPath := flags.String("db", defaultDBPath, "SQLite database path")
	flags.Usage = func() {
		fmt.Fprintln(stderr, "Usage: rxs add [-db PATH] URL")
		fmt.Fprintln(stderr, "\nOptions:")
		flags.PrintDefaults()
	}
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if flags.NArg() != 1 {
		return errors.New("usage: rxs add [-db PATH] URL")
	}
	return addFeed(ctx, *dbPath, flags.Arg(0), stdout)
}

func addFeed(ctx context.Context, dbPath, feedURL string, output io.Writer) error {
	repository, err := openStore(dbPath)
	if err != nil {
		return err
	}
	defer repository.Close()

	source, err := repository.AddFeed(ctx, feedURL)
	if err != nil {
		return err
	}
	result := feedservice.NewService(repository, feedservice.NewClient(version)).Refresh(ctx, source.ID)
	if result.Err != nil {
		return fmt.Errorf("feed %q was added, but its initial refresh failed: %w", source.URL, result.Err)
	}
	refreshed, err := repository.Feed(ctx, source.ID)
	if err != nil {
		return fmt.Errorf("load added feed: %w", err)
	}
	if _, err := fmt.Fprintf(output, "Added %s (%d new article(s))\n", refreshed.Title, result.Added); err != nil {
		return fmt.Errorf("write result: %w", err)
	}
	return nil
}

func openStore(dbPath string) (*store.Store, error) {
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o700); err != nil {
		return nil, fmt.Errorf("create data directory: %w", err)
	}
	return store.Open(dbPath)
}

func databasePath() (string, error) {
	dir, err := platform.DataDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "rxs.db"), nil
}
