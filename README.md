# pronto

PR triage dashboard and review queue. Get in the flow of reviewing PRs quickly: prioritize incoming PRs, track authored PR progress, and inspect detailed score breakdowns.

## Installation

### Homebrew

```sh
brew install --cask kalverra/tap/pronto
```

### mise

```sh
mise use -g github:kalverra/pronto@latest
```

### Go

```sh
go install github.com/kalverra/pronto/cmd/pronto@latest
```

### Binary Releases

Download pre-compiled binaries for macOS and Linux from [GitHub Releases](https://github.com/kalverra/pronto/releases).

## Usage

Launch interactive TUI:

```sh
pronto        # Launch the TUI
pronto --help # Show help
```

### AI Agents

pronto embeds a dedicated skill guide for AI coding agents. Run `pronto agent` to print setup instructions, socket protocols, and CLI recipes:

```sh
pronto agent
```

## Config

See [configuration](docs/configuration.md) for all config values and details. Below are some commonly-tuned settings.

### Enable Notifications

```sh
pronto notify setup
```

This builds and installs the helper (needs only the free Xcode command line tools — `xcode-select --install` — no Apple Developer account), registers it with LaunchServices, requests the one-time notification permission, and sends a test banner. It's safe to run again: each step is skipped when already satisfied.

Configure notifications in `~/.config/pronto/pronto.toml`:

```toml
[notifications]
popups = true
sound  = true

[notifications.sounds]
ci_passed = "Glass"   # any macOS system sound name, or "default"
```

Check the setup and send a test notification:

```sh
pronto notify        # show config, backends, native helper state, and per-trigger assets
pronto notify --test # deliver a test notification through the configured channels
```

### Category Rules

Two config sections route PRs automatically, and both use the same rule shape:

- `[focus]` auto-focuses matching PRs (★) in the **Focus** tab. PRs you toggle with `f` always stay focused.
- `[priority]` moves matching incoming PRs out of **Inbox** and into the **Priority** tab. By default this includes PRs whose review is requested from you personally (not only via a team) or that are assigned to you.

Configure category rules in `~/.config/pronto/pronto.toml`:

```toml
[focus]
exclude_bots = true              # default: bot-authored PRs never match
authors      = ["alice"]
repos        = ["org/critical-service"]

[priority]
direct_requests = true           # default: direct review requests + assignments
exclude_bots    = true           # default: bots stay in Inbox, even if requested directly
authors         = ["bob"]

[[priority.rules]]
repo        = "org/app"          # optional: scope the rule to one repo
keywords    = ["security"]       # PR title contains (case-insensitive)
files       = ["go.mod", "*.proto"]  # file name or glob
directories = ["infra"]          # any changed file under this directory
regex       = ['^migrations/.*\.sql$']  # RE2, matched against each changed file path
```

A PR matches a section if it matches any top-level `authors`/`repos` entry or any rule. Within a rule, `repo` must match, and then any one criterion is enough. A rule with only `repo` set matches every PR in that repo. A stack that has any matching PR moves to Priority as a whole.
