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

### Enable Notifications

<details>

<summary>macOS</summary>

pronto can post desktop notifications when CI finishes, a review lands, a PR hits a merge conflict, or a PR merges.

1. Install [terminal-notifier](https://github.com/julienxx/terminal-notifier) for best experience
2. Configure notifications in `~/.config/pronto/pronto.toml`:

  ```toml
  [notifications]
  popups = true
  sound  = true
  ```

3.Check the setup and send a test notification:

  ```sh
  pronto notify        # show config, backends, and per-trigger assets
  pronto notify --test # deliver a test notification through the configured channels
  ```

`pronto notify --test` exits non-zero if delivery fails. If it reports `Sent` but no banner appears, macOS suppressed it — allow **terminal-notifier** under System Settings → Notifications, and turn off Focus / Do Not Disturb. Delivery failures during normal runs are recorded in the log:

</details>

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
