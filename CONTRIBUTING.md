# Contributing to pronto

## Requirements

pronto manages its toolchain with [mise](https://mise.jdx.dev/). Ensure `mise` is installed, then bootstrap dependencies:

```sh
mise install # Install all deps
mise tasks # List available tasks
```

## Reporting Issues

If you encounter a bug, crash, or unexpected behavior:

1. **Search existing issues**: Check [GitHub Issues](https://github.com/kalverra/pronto/issues) to see if it is already tracked.
2. **File a bug report**: Open an issue using the [Bug Report Template](.github/ISSUE_TEMPLATE/bug_report.md) or the GitHub CLI:

   ```sh
   gh issue create --template bug_report.md
   ```

3. **Include details**:
   - Reproduction steps and commands executed.
   - Terminal logs, daemon logs (`~/.local/state/pronto/pronto.log`), or stack traces.
   - Environment info (`pronto --version`, OS/architecture, daemon vs standalone mode).

For new ideas or enhancements, use the [Feature Request Template](.github/ISSUE_TEMPLATE/feature_request.md).
