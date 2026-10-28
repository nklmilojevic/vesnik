# Changelog

## v0.4.0 - 2026-10-28

This release lets you sign in to Claude from the review. You do not need to quit vesnik to renew an expired login.

### New features

- The review now signs you in again when the claude login expired, so `r` no longer fails with the same error. `r` runs `claude auth login` in the terminal, then regenerates the notes.

## v0.3.0 - 2026-10-23

This release adds shell completions to the Nix package. It also fixes how vesnik finds the last release.

### New features

- The Nix package now installs shell completions for bash, fish and zsh. Your shell can now complete vesnik commands after you install the package.

### Fixes

- vesnik now ignores tags that HEAD cannot reach, so it picks the correct last release and next version. It stops pull request lookups at the first rate-limit error, which saves your GitHub API quota.

## v0.2.3 - 2026-10-21

This release updates the Anthropic SDK that vesnik uses. No commands, flags, or config fields change.

## v0.2.2 - 2026-10-20

This release fixes the terminal user interface (TUI). The TUI now keeps your terminal clean and follows the background colour.

### Fixes

- The TUI stays on the alternate screen until it quits, so no frame is drawn on your terminal. It also updates its colours when the terminal background changes.

## v0.2.1 - 2026-10-20

This release fixes release plans for Cargo workspace members. It also stops the test suite from changing a developer's repository when a git hook runs it.

### Fixes

- Release plans for a workspace member now list the `Cargo.lock` that changes, and resolve paths correctly with `-C`. The test suite no longer changes the developer's Git repository when a git hook runs it.

## v0.2.0 - 2026-10-20

This release gives the user more control over the suggested version bump. The user can now limit the bump and set a fallback bump.

### New features

- Add the `max_bump` config field. It limits the bump that the model suggests, so a project that never changes its major version does not get a major bump. An explicit `--bump` is not limited.
- Add the `--default-bump` flag. It sets the bump that the tool uses when the model is not available.

## v0.1.1 - 2026-10-20

This release makes error messages from failed `claude` and `codex` runs shorter and easier to read.

### Fixes

- Error messages from `claude` and `codex` failures now show only the last non-empty line, shortened. Before, a failing `codex` run printed the whole startup banner, and the real error was hard to find.

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
