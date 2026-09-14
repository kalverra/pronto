# pronto agent skill

Teach a coding agent to monitor GitHub pull requests through the pronto daemon.

## Setup

AI agents can self-bootstrap by running `pronto agent` to print this guide directly.
A daemon must be running for live queries. Start it in a persistent pane/process:

```sh
pronto serve                # foreground; one poller for all consumers
```

Socket: `PRONTO_SOCKET_PATH` env, else `~/.config/pronto/pronto.sock`.
All CLI wrappers fail fast with a clear error when the daemon is down.

## Layers

| Layer               | Use for                            |
| ------------------- | ---------------------------------- |
| `pronto agent`      | Agent self-bootstrap instructions  |
| CLI wrappers        | One-shot waits, scripted checks    |
| `pronto api schema` | Machine-readable protocol contract |
| Raw socket (NDJSON) | Long-lived event streams           |

## Commands

```sh
# Print agent skill instructions (self-bootstrap)
pronto agent

# Block until CI settles on a PR; exit 0 on event, 2 on timeout
pronto wait org/repo#123 --until ci_settled --timeout 30m

# Other --until values: merged, review_received, or any event type
pronto wait #123 --until merged

# Stream events as JSONL (Ctrl-C to stop)
pronto watch --json                        # everything
pronto watch org/repo#123 --json           # one PR
pronto watch --types ci_failed,conflict    # filtered

# Inspect the protocol
pronto api schema
```

## Event types

Eight types: PR-scoped (`ci_passed`, `ci_failed`, `conflict`,
`review_received`, `pr_merged`, `pr_added`, `pr_removed`) and the
queue-wide `queue_refreshed`. Full vocabulary with payloads and filter
semantics: [events.md](events.md) (generated; do not edit).
JSON shapes for `queue.snapshot` / `pr.get` results: [model.md](model.md).

## Raw socket protocol

NDJSON over a Unix domain socket. Request:

```json
{ "id": "req_1", "method": "queue.snapshot", "params": {} }
```

Response echoes `id` with `result` or `error:{code,message}`:

```json
{"id":"req_1","result":{"queue":{...},"fetched_at":"...","age_seconds":12.3,"dropped_events":0}}
```

Methods: `ping`, `queue.snapshot`, `queue.refresh`, `pr.get`, `events.subscribe`.
`queue.refresh` triggers an immediate poll and returns the current snapshot with `refreshing: true`.
Unknown methods return `unsupported_method`; ignore unknown result fields.

`events.subscribe` acknowledges, then pushes event lines on the same connection:

```json
{"id":"sub_1","method":"events.subscribe","params":{"subscriptions":[{"pr":42}]}}
{"id":"sub_1","result":{"type":"subscribed","subscriptions":1}}
{"seq":7,"type":"ci_failed","ts":"...","repo":"org/repo","pr":42,"title":"..."}
```

Subscription filters (all optional, AND within one filter, OR across filters):
`types` (array), `repo` (owner/name), `pr` (number). One connection = one
subscriber; events arrive in emit order. No replay: subscriptions start at
ack time; bootstrap state with `queue.snapshot` first, then subscribe, then
apply buffered events (see `pronto api schema` for full schema rationale).

Full contract: `pronto api schema`.

## Recommended agent flow

1. Self-bootstrap: run `pronto agent` to view these instructions.
2. `pronto serve` in a persistent pane (skip if already running).
3. One-shot: `pronto wait <ref> --until <condition> --timeout <dur>`; exit
   code 2 means timeout, anything else means the event fired.
4. Continuous: open `events.subscribe` on a dedicated connection and act on
   pushed lines.

## Troubleshooting & reporting issues

If pronto fails, crashes, or returns unexpected errors:

- Check daemon logs: `~/.local/state/pronto/pronto.log`.
- Report issues via GitHub CLI:

  ```sh
  gh issue create --repo kalverra/pronto --template bug_report.md
  ```
