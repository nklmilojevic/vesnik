# vesnik

vesnik cuts releases with a model's help and a human's approval. It reads the
commits and merged pull requests since the last tag, asks a model for release
notes and a version bump, and shows both in a review screen. Nothing is
written until you accept. Then it bumps the version files, updates
`CHANGELOG.md`, commits, tags, pushes, and publishes the GitHub Release.

```
$ vesnik release
```

## Why

Release tools that read conventional commits are predictable but write notes
nobody wants to read. Asking a model writes better notes, but one-shot
scripts publish whatever comes back. vesnik keeps both halves: the model
drafts the notes and suggests the bump with a reason, the commit types set a
floor it warns about, and you decide.

## Install

```sh
# Nix
nix run github:nklmilojevic/vesnik -- --help
nix profile install github:nklmilojevic/vesnik

# Prebuilt Nix binaries come from https://vesnik.cachix.org; the flake offers
# it, or add it permanently with `cachix use vesnik`.

# Go
go install github.com/nklmilojevic/vesnik/cmd/vesnik@latest
```

Binaries for Linux and macOS are attached to every
[GitHub Release](https://github.com/nklmilojevic/vesnik/releases).

## Usage

```sh
vesnik plan                 # show the next release, change nothing
vesnik plan --at v1.2.0      # replay a past release: what vesnik would pick vs. what shipped
vesnik release              # plan, review, publish
vesnik release --pre rc     # v1.3.0-rc.1, then rc.2, …
vesnik release              # after an rc: promotes to v1.3.0, notes cover every rc
vesnik release -p api       # only the api package of a monorepo
vesnik notes                # notes for the unreleased commits, to stdout
vesnik notes v1.2.0         # notes for a past tag
vesnik renote v1.2.0        # regenerate a published release's description
vesnik init                 # a model reads the repo and writes .vesnik.yaml
vesnik init --no-ai         # detect packages only
```

In the review screen: `enter` accepts, `e` edits the notes in `$EDITOR`, `r`
asks the model again, `b` cycles the bump, `s` skips a package, `q` aborts.

Releases start from a clean, up-to-date release branch. If anything fails
before the push, vesnik resets the branch and deletes the new tags.

## Models

| Provider    | Uses                                       | Credentials                                   |
| ----------- | ------------------------------------------ | --------------------------------------------- |
| `claude`    | the `claude` CLI, no tools or settings     | your Claude login                             |
| `codex`     | `codex exec`, read-only sandbox            | your Codex login                              |
| `anthropic` | the Messages API with structured outputs   | `ANTHROPIC_API_KEY` or an `ant auth login`    |
| `vertex`    | Claude on Vertex AI                        | Google ADC and `GOOGLE_CLOUD_PROJECT`         |

Every provider answers in the same JSON schema, so the notes never depend on
parsing free text. The default is `claude` with `claude-sonnet-5-5`.

GitHub access uses `GH_TOKEN`, `GITHUB_TOKEN`, or `gh auth token`.

## Ecosystems

The git tag is the source of truth for the current version. vesnik writes
the new version into the package's files:

| Ecosystem | Files                                                         |
| --------- | ------------------------------------------------------------- |
| `cargo`   | `Cargo.toml` (`[package]` or `[workspace.package]`), `Cargo.lock` |
| `node`    | `package.json`, `package-lock.json`; tag only without `version` |
| `php`     | `composer.json` when it has a `version`; otherwise tag only   |
| `go`      | tag only                                                      |
| `none`    | tag only                                                      |

Edits are surgical: indentation, key order and comments stay as they were.
Extra files take a regex with a `v` group, or a key path in JSON, YAML or
TOML.

## Configuration

`vesnik init` writes a starter file. It detects packages and ecosystems,
then a model reads the README, manifests, release recipes, workflow
triggers, recent commits and version-looking lines (tracked files only) and
suggests the product line, style rules, release branch, changelog and
version files. vesnik checks every suggestion against the repository before
writing it: the branch must exist and each regex must match its file.

Every field in `.vesnik.yaml` is optional. Without the file, the repository
is one package tagged `v{version}`.

```yaml
provider: claude
model: claude-sonnet-5-5

style:
  preset: ste100            # ste100, keepachangelog or casual
  product: "a terminal UI for Kubernetes"
  sections: [New features, Fixes, Other changes]
  rules: ["Call it a pod, not a Pod."]
  prompt_file: ""           # a text/template that replaces the preset

github:
  append_generated_notes: true   # GitHub's PR list under the notes

changelog:
  enabled: true             # false: tag-only releases make no commit
  file: CHANGELOG.md

breaking_bumps_minor: true  # below 1.0.0, breaking changes bump minor
max_bump: minor             # cap the model's bump, e.g. for apps that stay on 1.x
branch: main                # defaults to origin's default branch
remote: origin

packages:                   # several packages release independently
  - name: api
    path: packages/api
    ecosystem: php
    tag: "api@v{version}"   # the default with several packages
    files:
      - file: config/app.php
        regex: "'version' => '(?P<v>[^']+)'"
      - file: chart/Chart.yaml
        key: appVersion
  - path: packages/web
```

`vertex.project` and `vertex.region` (default `global`) configure Vertex AI.

`ai.on_error` decides what happens when the model fails (CLI missing, API
error, no network). `fail` (the default) stops before anything changes.
`plain` releases anyway: the version comes from `--bump` or the commit
types, the GitHub Release carries GitHub's generated notes, and a changelog
lists the commit subjects. The review shows a warning, and `r` retries the
model.

`vesnik release --default-bump minor` sets the bump used when the model is
unavailable; otherwise the commit types decide it. `--bump` always wins over
the model and is not capped by `max_bump`.

## Exit codes

Scripts that wrap `vesnik release` can tell whether falling back to another
release path is safe:

| Code | Meaning                                                           |
| ---- | ----------------------------------------------------------------- |
| `0`  | released, or nothing to release                                   |
| `1`  | failed before changing anything; safe to fall back                |
| `3`  | aborted in the review or interrupted; nothing was tagged          |
| `4`  | tags are pushed but a GitHub Release failed; run `vesnik renote`  |

## License

MIT
