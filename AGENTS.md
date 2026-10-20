# AGENTS.md

Rules for AI agents and humans working on vesnik. `docs/design.md` explains
the package layout and the release flow.

## Local commands

Every development tool comes from the Nix dev shell: `nix develop`, or
`direnv allow` once to load it on `cd`. Do not install tools any other way.
The `Justfile` tasks match CI.

```sh
just check          # lint + tidy-check + tests
just lint           # golangci-lint
just test           # go test -race ./...
just fmt            # golangci-lint fmt + nixpkgs-fmt
just snapshot       # build every release target with GoReleaser
just nix-check      # build and test the Nix package
```

The dev shell installs the lefthook hooks on first entry. Never commit with
`--no-verify`.

## Code

- Tests never touch the network or the developer's git config. Use
  `gittest.New` for repositories, a bare repo as `origin` for pushes, and the
  fake providers and forges in the existing tests.
- Every user-visible behaviour change gets a test that drives it through the
  public path: a `release.Env` method, a TUI key press, or a CLI command.
- vesnik shells out to `git` on purpose so users' signing, hooks and
  credential helpers apply. Do not replace it with a git library.
- Version files are edited in place. Never re-marshal a user's manifest.
- When `go.mod` changes, run `go mod tidy` and update `vendorHash` in
  `package.nix` (`nix build` prints the new hash).
- Keep go.mod's `go` directive at or below the Go in the flake's nixpkgs;
  the dev shell, CI and the package all build with that Go.

## Docs that must stay in sync

- A new command or flag: `README.md` usage and the command's help text.
- A new config field: `README.md` configuration and `internal/config`.
- A new ecosystem or provider: the tables in `README.md`.

## Git

- Base branch is `main`. Conventional commits: `feat:`, `fix:`, `docs:`,
  `refactor:`, `test:`, `chore:`. The body says why.
- Releases are cut with vesnik itself: `vesnik release`. The release
  workflow attaches binaries and warms the Nix cache.
