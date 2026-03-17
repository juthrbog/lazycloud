# LazyCloud

A terminal user interface (TUI) for browsing, managing, and interacting with AWS services and resources — without leaving your terminal. Built with Go and the [Charm](https://charm.sh) ecosystem, [inspired by](#inspired-by) tools like lazygit, k9s, and claws.

## Features

- Browse and manage AWS resources from your terminal
- Stack-based navigation with drill-down into resource details
- Filterable, sortable tables with vim-style keybindings
- Syntax-highlighted content viewer with visual line selection and yank to clipboard
- In-app event log for troubleshooting without leaving the TUI
- Multiple AWS profile and region support with fuzzy-search pickers
- 4 color themes (Catppuccin, Dracula, Nord, Tokyo Night) — switchable at runtime
- Nerd Font icons with Unicode fallbacks
- Open resources in `$EDITOR` directly from the TUI
- TOML config file with XDG base directory support
- LocalStack integration for local development

## Getting Started

```bash
# Build
go build -o lazycloud .

# Run against your default AWS profile
./lazycloud

# Specify a profile and region
./lazycloud --profile staging --region us-west-2

# Run against LocalStack
./lazycloud --endpoint http://localhost:4566
```

### With Taskfile

```bash
task deps              # download Go dependencies
task build             # build the binary
task run               # run against real AWS
task localstack:seed   # populate LocalStack with test data
task dev               # run against LocalStack
```

### Keybindings

**Global**

| Key | Action |
|-----|--------|
| `j`/`k` or arrows | Navigate up/down |
| `enter` | Drill into resource |
| `esc` | Go back |
| `/` | Filter/search |
| `r` | Refresh |
| `L` | Event log |
| `P` | Switch AWS profile |
| `R` | Switch AWS region |
| `T` | Switch theme |
| `q` | Quit |

**Content Viewer**

| Key | Action |
|-----|--------|
| `j`/`k` | Move cursor |
| `g`/`G` | Jump to top/bottom |
| `ctrl+d`/`ctrl+u` | Half-page down/up |
| `V` | Visual line select |
| `y` | Yank to clipboard |
| `e` | Open in `$EDITOR` |
| `n` | Toggle line numbers |

## Configuration

LazyCloud uses a TOML config file. Generate the default config with:

```bash
./lazycloud --init-config
```

This creates `~/.config/lazycloud/config.toml` (or `$XDG_CONFIG_HOME/lazycloud/config.toml`):

```toml
[aws]
# profile = "default"
# region = "us-east-1"
# endpoint = ""

[display]
theme = "catppuccin"    # catppuccin, dracula, nord, tokyonight
nerd_fonts = true       # false for plain Unicode fallbacks

[log]
# file = "/tmp/lazycloud.log"
```

Settings are applied in order of precedence: **config file < env vars < CLI flags**.

Use `--config path` to specify a custom config file location.

## Supported Clouds

Only **AWS** is supported at this time. Other cloud providers may be added in the future.

## Tech Stack

- **Language:** Go
- **TUI Framework:** [Bubble Tea v2](https://github.com/charmbracelet/bubbletea) (`charm.land/bubbletea/v2`)
- **Styling:** [Lip Gloss v2](https://github.com/charmbracelet/lipgloss) (`charm.land/lipgloss/v2`)
- **Components:** [Bubbles v2](https://github.com/charmbracelet/bubbles) (`charm.land/bubbles/v2`)
- **Syntax Highlighting:** [Chroma](https://github.com/alecthomas/chroma)
- **Config:** [TOML](https://github.com/pelletier/go-toml)
- **AWS SDK:** [aws-sdk-go-v2](https://github.com/aws/aws-sdk-go-v2)
- **Task Runner:** [Taskfile](https://taskfile.dev)
- **Local AWS:** [LocalStack](https://github.com/localstack/localstack)

## Inspired By

- [lazygit](https://github.com/jesseduffield/lazygit) — Git TUI
- [lazydocker](https://github.com/jesseduffield/lazydocker) — Docker TUI
- [k9s](https://github.com/derailed/k9s) — Kubernetes TUI
- [claws](https://github.com/clawscli/claws) — AWS TUI

## License

Licensed under the [Apache License 2.0](LICENSE).
