# Contributing to claudia

Found a bug or have an idea? [Open an issue](https://github.com/MartyJRE/claudia/issues/new/choose).
The forms ask for what you need to be specific: steps, `claudia version`, your
setup.

## Checks

CI runs on every push to `main` and every PR. To check locally before pushing:

```sh
gofmt -l . && go mod tidy -diff && go vet ./... && go test -race ./...
```

The tmux tests start a private tmux server, so they never touch your sessions.
`TestPlanReviewInTmux` goes end to end: it builds the binary, runs a stand-in
Claude plan prompt (`internal/tmux/testdata/fakeplan.py`, needs `python3`) in one
pane and `claudia plan` in another, types comments through tmux and checks the
exact bytes the prompt receives. `go test -short` skips it.

## Commits and releases

Commit subjects start with a lowercase scope, e.g. `board: …` or `sessions: …`,
and say what changed for the user. Every green push to `main` is released: CI
tags it with the next patch version and goreleaser publishes the binaries, with
notes grouped by that scope. A `Release: minor` or `Release: major` trailer in
any commit since the last release bumps that part instead.

## Screenshots

The screenshots in `assets/` are rendered from the real UI with demo data:

```sh
CLAUDIA_PREVIEWS=assets go test ./internal/ui -run TestWritePreviews
```

## Notification helper

The notification helper in `notifier/` is a Swift package: its logic lives in
`NotifierCore`, with the AppKit and UserNotifications wiring in the thin
`claudia-notify` executable. `notifier/coverage.sh` runs its tests and prints
NotifierCore's coverage; `notifier/build.sh /tmp/Claudia.app` builds the app
somewhere other than `~/Applications`.
