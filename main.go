package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/juthrbog/lazycloud/internal/app"
	"github.com/juthrbog/lazycloud/internal/config"
	"github.com/juthrbog/lazycloud/internal/eventlog"
	"github.com/juthrbog/lazycloud/internal/ui"
	"github.com/juthrbog/lazycloud/internal/version"
)

// opsShutdownWait is how long the app waits for cancelled background
// operations when it quits. A cancelled download removes its partial file
// within this time.
const opsShutdownWait = 5 * time.Second

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// options holds the command line flags.
type options struct {
	profile, region, endpoint, logFile, theme, configPath string
	// nerdFonts is nil when the user did not set --no-nerd-fonts.
	// --no-nerd-fonts=false sets it to true, so a flag can turn the icons
	// back on after the config file turned them off.
	nerdFonts   *bool
	readWrite   bool
	initConfig  bool
	showVersion bool
}

// parseFlags reads the command line flags in args.
func parseFlags(args []string, stderr io.Writer) (options, error) {
	var o options
	fs := flag.NewFlagSet("lazycloud", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&o.profile, "profile", "", "AWS profile")
	fs.StringVar(&o.region, "region", "", "AWS region")
	fs.StringVar(&o.endpoint, "endpoint", "", "AWS endpoint override (for LocalStack)")
	fs.StringVar(&o.logFile, "log", "", "path to log file for debugging")
	fs.StringVar(&o.theme, "theme", "", "color theme: auto (default), "+strings.Join(ui.ThemeOrder, ", "))
	noNerd := fs.Bool("no-nerd-fonts", false, "disable Nerd Font icons (--no-nerd-fonts=false enables them)")
	fs.StringVar(&o.configPath, "config", "", "path to config file")
	fs.BoolVar(&o.readWrite, "read-write", false, "start in ReadWrite mode (default: ReadOnly)")
	fs.BoolVar(&o.initConfig, "init-config", false, "write default config file and exit")
	fs.BoolVar(&o.showVersion, "version", false, "print version and exit")
	if err := fs.Parse(args); err != nil {
		return o, err
	}
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "no-nerd-fonts" {
			on := !*noNerd
			o.nerdFonts = &on
		}
	})
	return o, nil
}

// printf writes a message for the user. A message that cannot be written
// has nowhere else to go, so the error is dropped.
func printf(w io.Writer, format string, args ...any) {
	_, _ = fmt.Fprintf(w, format, args...)
}

// run starts the app and returns the exit code. The deferred calls of run,
// for example the close of the log file, run before main exits.
func run(args []string, stdout, stderr io.Writer) int {
	o, err := parseFlags(args, stderr)
	if errors.Is(err, flag.ErrHelp) {
		return 0
	}
	if err != nil {
		return 2
	}

	if o.showVersion {
		printf(stdout, "lazycloud %s (commit %s, built %s)\n", version.Version, version.Commit, version.Date)
		return 0
	}

	// Write default config and exit
	if o.initConfig {
		path := o.configPath
		if path == "" {
			path = config.DefaultPath()
		}
		written, err := config.WriteDefault(path)
		if err != nil {
			printf(stderr, "Error writing config: %v\n", err)
			return 1
		}
		if !written {
			printf(stdout, "Config file %s already exists. It was not changed.\n", path)
			return 0
		}
		printf(stdout, "Config written to %s\n", path)
		return 0
	}

	// Load config: file → env → flags (increasing precedence)
	cfg, warnings, err := config.Load(o.configPath)
	if err != nil {
		printf(stderr, "Error loading config: %v\n", err)
		return 1
	}
	for _, w := range warnings {
		printf(stderr, "Warning: %s\n", w)
	}
	cfg.ApplyEnv()
	cfg.ApplyFlags(o.profile, o.region, o.endpoint, o.logFile, o.theme, o.nerdFonts)

	// Apply theme. The auto theme starts dark. The app switches to the light
	// theme when the terminal reports a light background.
	themeName := strings.ToLower(cfg.Display.Theme)
	if themeName == ui.AutoTheme {
		themeName = ui.AutoThemeName(true)
	}
	if t, ok := ui.Themes[themeName]; ok {
		ui.ActiveTheme = t
		ui.RebuildStyles()
	} else {
		printf(stderr, "Unknown theme: %s (available: %s, %s)\n", cfg.Display.Theme, ui.AutoTheme, strings.Join(ui.ThemeOrder, ", "))
		return 1
	}

	ui.UseNerdFonts = cfg.Display.NerdFonts

	if o.readWrite {
		ui.SetReadOnly(false)
	}

	if cfg.Log.File != "" {
		f, err := os.OpenFile(cfg.Log.File, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
		if err != nil {
			printf(stderr, "Error opening log file: %v\n", err)
			return 1
		}
		defer f.Close()
		eventlog.SetOutput(f)
	}
	// The warnings also go to the event log, because the screen of the app
	// covers stderr.
	for _, w := range warnings {
		eventlog.Warnf(eventlog.CatApp, "Config: %s", w)
	}

	rootModel := app.New(cfg)
	for _, w := range rootModel.StartWarnings() {
		printf(stderr, "Warning: %s\n", w)
	}
	p := tea.NewProgram(rootModel)
	_, runErr := p.Run()
	// On every way out, also a signal or an error, stop the background
	// operations and give them a moment to clean up, for example to remove
	// a partial download.
	rootModel.Ops().CancelAll()
	rootModel.Ops().Wait(opsShutdownWait)
	if runErr != nil {
		printf(stderr, "Error: %v\n", runErr)
		return 1
	}
	return 0
}
