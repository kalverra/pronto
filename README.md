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

## Config

See [configuration](docs/configuration.md) for details.

### Enable Notifications

<details>

<summary>macOS</summary>

pronto can post desktop notifications when CI finishes, a review lands, a PR hits a merge conflict, or a PR merges.

By default (`notifications.mode = "native"`) pronto posts through a bundled, ad-hoc signed helper app for Pronto-branded banners, click-to-open PR, and per-PR grouping. Run the guided setup once:

```sh
pronto notify setup
```

This builds and installs the helper (needs only the free Xcode command line tools — `xcode-select --install` — no Apple Developer account), registers it with LaunchServices, requests the one-time notification permission, and sends a test banner. It's safe to run again: each step is skipped when already satisfied.

If the helper isn't installed or authorized (or you denied it in System Settings), native mode falls back to terminal delivery and the TUI shows a one-line hint on how to fix it. With `notifications.mode = "terminal"`, pronto always shells out to [terminal-notifier](https://github.com/julienxx/terminal-notifier) (with an `osascript` fallback) — no setup required, but banners are attributed to the helper binary rather than Pronto.

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

`pronto notify --test` exits non-zero if delivery fails. If it reports `Sent` but no banner appears, macOS suppressed it — allow **Pronto** (native mode) or **terminal-notifier** (terminal mode) under System Settings → Notifications, and turn off Focus / Do Not Disturb. Delivery failures during normal runs are recorded in the log:

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
