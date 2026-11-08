package main

import (
	"flag"
	"fmt"
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
	os.Exit(run())
}

// run starts the app and returns the exit code. The deferred calls of run,
// for example the close of the log file, run before main exits.
func run() int {
	// CLI flags
	profile := flag.String("profile", "", "AWS profile")
	region := flag.String("region", "", "AWS region")
	endpoint := flag.String("endpoint", "", "AWS endpoint override (for LocalStack)")
	logFile := flag.String("log", "", "path to log file for debugging")
	theme := flag.String("theme", "", "color theme: auto (default), "+strings.Join(ui.ThemeOrder, ", "))
	noNerd := flag.Bool("no-nerd-fonts", false, "disable Nerd Font icons")
	configPath := flag.String("config", "", "path to config file")
	readWrite := flag.Bool("read-write", false, "start in ReadWrite mode (default: ReadOnly)")
	initConfig := flag.Bool("init-config", false, "write default config file and exit")
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Printf("lazycloud %s (commit %s, built %s)\n", version.Version, version.Commit, version.Date)
		return 0
	}

	// Write default config and exit
	if *initConfig {
		path := *configPath
		if path == "" {
			path = config.DefaultPath()
		}
		if err := config.WriteDefault(path); err != nil {
			fmt.Fprintf(os.Stderr, "Error writing config: %v\n", err)
			return 1
		}
		fmt.Printf("Config written to %s\n", path)
		return 0
	}

	// Load config: file → env → flags (increasing precedence)
	cfg, err := config.Load(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading config: %v\n", err)
		return 1
	}
	cfg.ApplyEnv()
	cfg.ApplyFlags(*profile, *region, *endpoint, *logFile, *theme, *noNerd)

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
		fmt.Fprintf(os.Stderr, "Unknown theme: %s (available: %s, %s)\n", cfg.Display.Theme, ui.AutoTheme, strings.Join(ui.ThemeOrder, ", "))
		return 1
	}

	if !cfg.Display.NerdFonts {
		ui.UseNerdFonts = false
	}

	if *readWrite {
		ui.SetReadOnly(false)
	}

	if cfg.Log.File != "" {
		f, err := os.OpenFile(cfg.Log.File, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error opening log file: %v\n", err)
			return 1
		}
		defer f.Close()
		eventlog.SetOutput(f)
	}

	rootModel := app.New(cfg)
	p := tea.NewProgram(rootModel)
	_, runErr := p.Run()
	// On every way out, also a signal or an error, stop the background
	// operations and give them a moment to clean up, for example to remove
	// a partial download.
	rootModel.Ops().CancelAll()
	rootModel.Ops().Wait(opsShutdownWait)
	if runErr != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", runErr)
		return 1
	}
	return 0
}
