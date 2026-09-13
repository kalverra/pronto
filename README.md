# pronto

PR triage dashboard and review queue. Get in the flow of reviewing PRs quickly: prioritize incoming PRs, track authored PR progress, and inspect detailed score breakdowns.

## Installation

### Homebrew

```sh
brew install kalverra/tap/pronto
```

### mise

```sh
mise use -g ubi:kalverra/pronto
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

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md) for local development setup, workflow commands, and project conventions. Read [docs/architecture.md](docs/architecture.md) for application architecture.
