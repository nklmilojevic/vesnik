# Changelog

## v0.1.0 - 2026-10-20

This first release of vesnik writes release notes and picks version bumps with AI. A person reviews the result before vesnik changes anything.

### New features

- Add vesnik. It reads the commits and merged pull requests since the last tag and asks a model for release notes and a semver bump. The model can be the `claude` or `codex` CLI, the Anthropic API, or Vertex AI.
- Review the notes and the bump in a TUI before vesnik writes anything. You keep control of each release.
- Bump version files in place for Cargo, Node, PHP, or custom files. Vesnik then writes a changelog, commits, tags, and pushes the changes atomically.
- Publish GitHub Releases from the same run.
- Use vesnik with monorepos and pre-release lines. You can promote a pre-release to a full release.
- Use a dry run to see what vesnik would do. Replay past releases and use `renote` to write the notes again.
- Run an AI-assisted `init` to set up a project.
- Use the plain fallback when the model is not available. Wrapper scripts can use the exit codes.
