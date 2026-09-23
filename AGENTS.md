# AGENTS

Create an maintain a simple CLI + TUI tool for a clean interface of PR statuses.

## Rules

- Still in alpha. Large, breaking changes fine to keep code clean and performant.
- Read docs/architecture.md for a high-level overview of the application. Update this file as necessary.
- Consider using `testing/synctest` for channel/timing tests. Socket/daemon tests cannot bubble (fd blocking).
- Wire protocol changes: edit `internal/events` (vocabulary) or `internal/server` (methods), then run `mise run generate`. `tools/gendocs` regenerates `docs/{events,config,model}.md` and `internal/events/schema.json` from `events.ValidTypes`, `events.ValidCodes`, and `server.Methods` (never edit those generated files). Staleness tests in `tools/gendocs` fail if regeneration is skipped.

## Layout

| Package              | Role                                                                                                 |
| -------------------- | ---------------------------------------------------------------------------------------------------- |
| `cmd/pronto`         | Cobra+fang CLI; bare invocation launches TUI                                                         |
| `internal/source`    | GitHub GraphQL fetching; `Source` interface is the test seam (`NewFixtureSource`)                    |
| `internal/model`     | Domain types (Queue, PullRequest, Checks, MergeStatus); JSON-tagged                                  |
| `internal/score`     | Ranking + explainable breakdowns                                                                     |
| `internal/tui`       | Bubbletea app                                                                                        |
| `internal/notify`    | Change detection (`Detector` → Triggers) + desktop notifications                                     |
| `internal/events`    | Event vocabulary, subscription filters, bus, wire schema                                             |
| `internal/server`    | NDJSON Unix socket API (`internal/server/server.go`)                                                 |
| `internal/daemon`    | Single poll loop feeding events + snapshots                                                          |
| `internal/profiling` | pprof + goroutine leak profile wiring (serve, daemon, TestMains)                                     |
| `internal/client`    | Socket client used by `watch`/`wait`                                                                 |
| `internal/cache`     | On-disk JSON cache (`Store` interface)                                                               |
| `internal/config`    | viper/TOML, env overrides (`PRONTO_*`); `Specs` table is the single source of truth                  |
| `tools/gendocs`      | `go generate` generator for `docs/{events,config,model}.md` + `internal/events/schema.json`          |
| `docs/`              | high-level docs on architecture, usage, and design; `events.md`/`config.md`/`model.md` are generated |

## Dev Commands

Run these commands to validate changes

```sh
# Basic checks
mise run lint
mise run test
mise run race

# Thorough checks for finding flakes/races
mise run test:thorough
mise run race:thorough

# Regenerate reference docs (docs/events.md, config.md, model.md) and
# the wire schema (internal/events/schema.json)
mise run generate
```
