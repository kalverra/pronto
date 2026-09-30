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

See [configuration](docs/config.md) for all config values and details. Below are some commonly-tuned settings.

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

[notifications.images]
ci_failed = "~/icons/red.png"   # override the default per-trigger icon
```

Check the setup and send a test notification:

```sh
pronto notify        # show config, backends, native helper state, and per-trigger assets
pronto notify --test # deliver a test notification through the configured channels
```

### What Notifies

You choose which events notify **per tab and per section**. Triggers:

| Trigger | Fires when |
| ------- | ---------- |
| `ci_passed` / `ci_failed` | checks flip to passing / failing |
| `conflict` | a PR newly has merge conflicts |
| `review_received` | someone reviews a PR (not you, not bots) |
| `pr_merged` / `pr_closed` | a PR is merged / closed without merging |
| `pr_opened` | a PR was created since the last poll |
| `merge_queue_entered` / `merge_queue_left` | a PR enters / leaves the merge queue |
| `new_commits` | someone other than you pushes to a PR |
| `entered` | a PR gains a tab or section, e.g. lands in Priority or becomes ready for your review |

Each tab (`focus`, `mine`, `priority`, `inbox`) has a `triggers` list, and each of its sections
(`attention`, `action_required`, `merge_queue`, `ready_to_merge`, `in_review`, `blocked`, `drafts`,
`stale`) can override it. Lists are tokens evaluated left to right: a trigger name adds it,
`!name` removes it, `all` adds everything, `inherit` (sections only) starts from the tab's set, and
`[]` means nothing. A PR notifies if *any* tab or section it sits in subscribes to the trigger.

Defaults:

| Tab | Notifies |
| --- | -------- |
| Focus | every event except `entered` |
| Mine | every event except `new_commits` and `entered` |
| Priority | `entered`: a PR newly assigned or directly requested of you, or one becoming ready for your review |
| Inbox | nothing |

```toml
# Mine: only CI and merge events, plus review activity while a PR is in review.
[notifications.mine]
triggers  = ["ci_failed", "ci_passed", "pr_merged"]
in_review = ["inherit", "review_received"]

# Priority: also tell me when a PR I'm requested on gets new commits or CI results,
# but stay quiet about stale ones.
[notifications.priority]
triggers = ["entered", "new_commits", "ci_passed"]
stale    = []

# Inbox: ping when a PR becomes ready for review.
[notifications.inbox]
attention = ["entered"]

# Focus: mute completely.
[notifications.focus]
triggers = []
```

Tab lists can also come from the environment, e.g. `PRONTO_NOTIFICATIONS_MINE_TRIGGERS="all,!new_commits"`.
Every event is still emitted on the event stream (`pronto watch`, `pronto wait`) regardless of policy;
only policy-selected ones show a desktop banner. The old `notifications.groups` key was removed.
See [configuration](docs/config.md#notification-policy) for the full key, default, and section reference.

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
