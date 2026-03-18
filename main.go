package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/juthrbog/lazycloud/internal/app"
	"github.com/juthrbog/lazycloud/internal/config"
	"github.com/juthrbog/lazycloud/internal/ui"
)

func main() {
	// CLI flags
	profile := flag.String("profile", "", "AWS profile")
	region := flag.String("region", "", "AWS region")
	endpoint := flag.String("endpoint", "", "AWS endpoint override (for LocalStack)")
	logFile := flag.String("log", "", "path to log file for debugging")
	theme := flag.String("theme", "", "color theme (catppuccin, dracula, nord, tokyonight)")
	noNerd := flag.Bool("no-nerd-fonts", false, "disable Nerd Font icons")
	configPath := flag.String("config", "", "path to config file")
	readWrite := flag.Bool("read-write", false, "start in ReadWrite mode (default: ReadOnly)")
	initConfig := flag.Bool("init-config", false, "write default config file and exit")
	flag.Parse()

	// Write default config and exit
	if *initConfig {
		path := *configPath
		if path == "" {
			path = config.DefaultPath()
		}
		if err := config.WriteDefault(path); err != nil {
			fmt.Fprintf(os.Stderr, "Error writing config: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Config written to %s\n", path)
		return
	}

	// Load config: file → env → flags (increasing precedence)
	cfg, err := config.Load(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading config: %v\n", err)
		os.Exit(1)
	}
	cfg.ApplyEnv()
	cfg.ApplyFlags(*profile, *region, *endpoint, *logFile, *theme, *noNerd)

	// Apply theme
	if t, ok := ui.Themes[strings.ToLower(cfg.Display.Theme)]; ok {
		ui.ActiveTheme = t
		ui.RebuildStyles()
	} else {
		fmt.Fprintf(os.Stderr, "Unknown theme: %s (available: catppuccin, dracula, nord, tokyonight)\n", cfg.Display.Theme)
		os.Exit(1)
	}

	if !cfg.Display.NerdFonts {
		ui.UseNerdFonts = false
	}

	if *readWrite {
		ui.ReadOnly = false
	}

	if cfg.Log.File != "" {
		f, err := os.OpenFile(cfg.Log.File, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error opening log file: %v\n", err)
			os.Exit(1)
		}
		defer f.Close()
	}

	rootModel := app.New(cfg)
	p := tea.NewProgram(rootModel)
	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
