package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime/debug"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/mattn/go-isatty"
	"github.com/polera/rxs/internal/app"
	feedservice "github.com/polera/rxs/internal/feed"
	"github.com/polera/rxs/internal/platform"
	"github.com/polera/rxs/internal/store"
	"github.com/polera/rxs/internal/ui"
	"github.com/polera/rxs/internal/upgrade"
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
	currentVersion := installedVersion(version)
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
		fmt.Fprintln(stderr, "       rxs [options] upgrade")
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
		fmt.Fprintf(stdout, "rxs %s (%s, %s)\n", currentVersion, commit, date)
		return nil
	}
	commandArgs := flags.Args()
	if len(commandArgs) > 0 {
		switch commandArgs[0] {
		case "add":
			return runAddCommand(context.Background(), commandArgs[1:], *dbPath, stdout, stderr)
		case "upgrade":
			return runUpgradeCommand(commandArgs[1:], currentVersion, stdout, stderr)
		default:
			return fmt.Errorf("unknown command %q", commandArgs[0])
		}
	}
	if interactiveTerminal() && offerUpgrade(currentVersion, os.Stdin, stdout, stderr) {
		return nil
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
	client := feedservice.NewClient(currentVersion)
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
	model.SetMarkReadOnScroll(config.Reading.MarkReadOnScroll)
	model.SetHideRead(config.Reading.HideRead)
	model.SetColorSchemeSaver(func(name string) error {
		return platform.SaveColorScheme(*configPath, name)
	})
	_, err = tea.NewProgram(model).Run()
	return err
}

func runUpgradeCommand(args []string, installed string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("rxs upgrade", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.Usage = func() { fmt.Fprintln(stderr, "Usage: rxs upgrade") }
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("upgrade does not accept arguments")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	result, err := upgrade.NewClient().Upgrade(ctx, installed, "")
	if err != nil {
		return err
	}
	if !result.Updated {
		fmt.Fprintf(stdout, "no upgrade available (installed %s; latest release %s)\n", result.Previous, result.Current)
		return nil
	}
	_, err = fmt.Fprintf(stdout, "upgraded rxs from %s to %s\n", result.Previous, result.Current)
	return err
}

func offerUpgrade(installed string, stdin io.Reader, stdout, stderr io.Writer) bool {
	if _, err := upgrade.Available(installed, installed); err != nil {
		return false
	}
	statePath, err := upgrade.StateFile()
	if err != nil {
		return false
	}
	state, err := upgrade.LoadState(statePath)
	if err != nil {
		state = upgrade.State{}
	}
	now := time.Now()
	if !state.ShouldCheck(now) {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	client := upgrade.NewClient()
	release, err := client.Latest(ctx)
	cancel()
	if err != nil {
		return false
	}
	state.CheckedAt = now
	state.LatestVersion = release.Version
	_ = upgrade.SaveState(statePath, state)
	available, err := upgrade.Available(installed, release.Version)
	if err != nil || !available {
		return false
	}

	fmt.Fprintf(stdout, "A new rxs release is available: %s -> %s\n", installed, release.Version)
	fmt.Fprint(stdout, "Upgrade now? [y/N] ")
	answer, _ := bufio.NewReader(stdin).ReadString('\n')
	answer = strings.ToLower(strings.TrimSpace(answer))
	if answer != "y" && answer != "yes" {
		state.DeferredUntil = now.Add(upgrade.DeferDuration)
		_ = upgrade.SaveState(statePath, state)
		fmt.Fprintln(stdout, "Upgrade deferred. Run `rxs upgrade` at any time.")
		return false
	}

	ctx, cancel = context.WithTimeout(context.Background(), 30*time.Second)
	result, err := client.UpgradeTo(ctx, installed, "", release)
	cancel()
	if err != nil {
		fmt.Fprintln(stderr, "rxs: upgrade failed:", err)
		return false
	}
	if result.Updated {
		fmt.Fprintf(stdout, "Upgraded rxs from %s to %s. Restart rxs to continue.\n", result.Previous, result.Current)
		return true
	}
	return false
}

func installedVersion(linked string) string {
	module := ""
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		module = info.Main.Version
	}
	return chooseInstalledVersion(linked, module)
}

func chooseInstalledVersion(linked, module string) string {
	if linked != "" && linked != "dev" {
		return linked
	}
	if module != "" && module != "(devel)" {
		return module
	}
	return linked
}

func interactiveTerminal() bool {
	stdin := os.Stdin.Fd()
	stdout := os.Stdout.Fd()
	return (isatty.IsTerminal(stdin) || isatty.IsCygwinTerminal(stdin)) &&
		(isatty.IsTerminal(stdout) || isatty.IsCygwinTerminal(stdout))
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
	result := feedservice.NewService(repository, feedservice.NewClient(installedVersion(version))).Refresh(ctx, source.ID)
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
