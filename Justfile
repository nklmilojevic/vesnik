set shell := ["bash", "-eu", "-o", "pipefail", "-c"]

# Run inside `nix develop` (or direnv), which provides every tool.

default:
    @just --list

# Lint, tidy-check and test: what CI runs.
check: lint tidy-check test

# Run golangci-lint.
lint:
    golangci-lint run

# Format Go code in place.
fmt:
    golangci-lint fmt
    nixpkgs-fmt flake.nix package.nix

# Run the tests with the race detector.
test:
    test -z "$(gofmt -l .)" || { gofmt -l .; exit 1; }
    go test -race ./...

# Fail when go.mod or go.sum are not tidy.
tidy-check:
    go mod tidy
    git diff --exit-code go.mod go.sum

# Lint the GitHub workflows.
actionlint:
    actionlint

# Build the binary into ./bin.
build:
    go build -o bin/vesnik ./cmd/vesnik

# Build every release target with GoReleaser, without publishing.
snapshot:
    goreleaser release --snapshot --clean

# Build the Nix package and run its tests.
nix-check:
    nix flake check --print-build-logs
