# Design

vesnik is a local-first release driver. A human runs it at a terminal, a
model drafts the notes and the bump, and the human accepts before anything is
written. CI does not cut releases; it only builds what a release publishes.

## Flow

`vesnik release` runs these steps. Steps 1 to 4 change nothing.

1. **Preflight** (`release.Env.Preflight`). The tracked tree is clean, the
   current branch is the release branch, it fast-forwards to the remote, and
   local equals remote.
2. **Plan** (`release.Env.Plan`). For each package, the latest tag and the
   latest final tag come from the package's tag template. The notes base is
   the last final tag for a final release, so a promoted pre-release covers
   every rc, or the latest tag for a pre-release. `collect` gathers the
   commits that touch the package path since the base, without release
   chores, and the merged pull requests behind them. A package without
   commits is skipped.
3. **Propose**. One model call per changed package (four at a time) returns
   a `Proposal`: bump, reasoning, and Markdown notes, constrained by one JSON
   schema for every provider. `version.Floor` reads the conventional commit
   types and the plan warns when the model's bump is lower.
4. **Review** (`tui`). The human accepts, edits, regenerates, changes the
   bump, skips packages, or aborts. `--dry-run` and `vesnik plan` print the
   plan instead; `--yes` accepts it.
5. **Execute** (`release.Env.Execute`). Write versions (`ecosystem`) and
   extra files, prepend each changelog, make one `chore(release):` commit,
   create one annotated tag per package, and push the branch and tags
   atomically. A failure before the push resets the branch and deletes the
   tags.
6. **Publish**. One GitHub Release per package. The body is the notes plus,
   by default, GitHub's generated pull request list. A failure here leaves
   the tags pushed; `vesnik renote <tag>` creates the missing release.

## Packages

| Package          | Job                                                              |
| ---------------- | ---------------------------------------------------------------- |
| `cmd/vesnik`     | `main`: signals and exit codes                                   |
| `internal/cli`   | Cobra commands and `init` detection                              |
| `internal/config`| `.vesnik.yaml`, defaults, validation                             |
| `internal/gitx`  | `git` subprocesses; `gittest` builds repositories for tests      |
| `internal/forge/github` | the GitHub REST calls vesnik needs                        |
| `internal/collect` | commits and pull requests per package                          |
| `internal/version` | semver bumps, pre-release lines, tag templates, the floor      |
| `internal/ecosystem` | in-place version edits for each ecosystem and file rule      |
| `internal/prompt` | style presets and prompt rendering                              |
| `internal/ai`    | the four providers behind one `Provider` interface               |
| `internal/changelog` | changelog sections and prepending                            |
| `internal/release` | plan, execute, notes, renote                                   |
| `internal/tui`   | the review screen                                                |

## Versions

The git tag is the source of truth. `version.Next(current, lastFinal, bump,
opts)` computes the next version:

- A final release applies the bump to the current version. Below 1.0.0, a
  major bump becomes a minor one when `breaking_bumps_minor` is on.
- `--pre rc` on a final version starts `X.Y.Z-rc.1` at the bumped version.
- `--pre rc` on an rc line continues it (`rc.2`). A different identifier
  restarts the counter. A bump larger than the one that started the line,
  measured from the last final release, moves the line to the new version.
- A final release on an rc line promotes it to `X.Y.Z`.

## Decisions

- **Shell out to git.** Users' commit and tag signing, hooks and credential
  helpers apply with no extra code.
- **Structured output everywhere.** `claude --json-schema`, `codex exec
  --output-schema`, and the Messages API's `output_config.format` all take the
  same schema, so no provider's text is parsed by hand.
- **Surgical file edits.** JSON is edited at the token offset, TOML by a
  table-aware line scan, YAML at the node's position. Formatting, key order
  and comments survive.
- **PHP applications are tag-only.** Most have no `version` in
  `composer.json`; `files` rules cover version constants in code.
- **One commit, many tags.** A monorepo release is one commit with one tag
  and one GitHub Release per package.
- **The Messages API asks for server-side refusal fallback** (`fallbacks:
  "default"`), so a declined request is retried on another model in the same
  call. Vertex AI does not support it.
