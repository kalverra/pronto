# Domain Glossary

Canonical vocabulary for pronto. Use these terms consistently in code, comments, and architectural design.

## Core Domain Concepts

### Queue

The aggregate collection of open pull requests relevant to the current user, split into `Authored` (created by the user) and `Inbox` (review requested, assigned, or team review requested).

### PullRequest

The domain representation of a GitHub pull request, encapsulating identity, git commit references, review decisions, CI checks rollup, merge state status, and branch relationships.

### Stack

A dependent chain of pull requests sharing a branch lineage (`model.StackInfo`). Stacks can be rendered collapsed (summarized with roll-up metrics and micro-status ribbons) or expanded (tree hierarchy).

### ActionStatus

The primary human-actionable lifecycle state of a pull request (`clean`, `needs_review`, `failing_ci`, `ci_running`, `changes_requested`, `conflict`, `behind`, `blocked`, `draft`, `queued`). Computed deterministically from PR fields by `model.PullRequest.ActionStatus()`.

### Section

Triage priority partitions displayed within TUI tabs (`attention`, `action_required`, `merge_queue`, `ready_to_merge`, `in_review`, `blocked`, `drafts`, `stale`).

### Focus

User-starred priority state for pull requests. Focus can be explicitly toggled by the user (persisted in `focus.json`) or automatically assigned through configured rules (`[focus]`).

### Scope

The logical location of a pull request within the triage hierarchy: a `Tab` (`focus`, `mine`, `priority`, `inbox`) optionally narrowed to a `Section`.

### Trigger

A discrete, notification-worthy state transition on a pull request (e.g., `ci_passed`, `ci_failed`, `review_received`, `pr_merged`, `pr_opened`, `pr_closed`, `merge_queue_entered`, `new_commits`, `entered`). Corresponds to event vocabulary in `internal/events`.

---

## Architectural Seams & Deep Modules

### Detector (`notify.Detector`)

The deep module that observes state transitions between two `model.Queue` snapshots. `Detect` absorbs scope classification, trigger detection, queue set-diffing (`pr_added`/`pr_removed`), and policy filtering, returning one `Delta`. `Seed` primes it from a warm-start queue without notifying.

### Table projection (`tui.tableProjection`)

The display model that turns a tab's items, sort strategy, focus pins, and stack fold state into an indexed list of rows (dividers, stack banners, collapsed stacks, items). Rendering, cursor navigation, scrolling, and mouse hit-testing all read it.

### Scheduler (`source.scheduler`, internal)

Pacing for `GraphQLSource`: PR activity tiers (`hot`, `active`, `recent`, `quiet`, `stale`), per-PR jitter, idle discovery backoff, and rate-limit budget scaling, producing the `NextFetch` deadline.

### Store (`cache.Store`)

The persistence seam: miss-tolerant, schema-stamped documents (`identity.json`, `queue.json`, `focus.json`, per-PR snapshots) written atomically via temp file + rename. `DiskStore` is the real implementation; `cachetest.Store` is the in-memory fake.
