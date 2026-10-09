# AGENTS.md

Guide for coding agents and contributors. Read this before changing anything.

## What this is

upkeep lets a program update itself from a manifest URL and keep itself
running under any supervisor. README.md is the model, docs/spec.md the
manifest format, docs/design.md the reasoning. The spec is the contract; code
follows it, not the other way round.

## Rules

1. Opt-in, no magic. Nothing updates without `--auto-update`. Do not add
   detection of TTYs, tmux, package managers or "what the user probably meant".
2. Works unsigned. Never make a signature a prerequisite for the hash tier.
3. Exec in place on Unix. Do not introduce a parent/child supervisor model;
   the PID must survive an update so launchd, systemd and nohup keep working.
4. Fail closed before the swap, never touch state the program owns, and keep
   `.prev` until the new version has written its healthy marker.
5. Standard library only unless a dependency is justified in docs/design.md.
6. `gofmt -w`, `go vet ./...`, `go test ./...` before every commit. CI runs
   the same on Linux and macOS.
7. Comments explain why, not what. One short line, only when it is not obvious.
8. Commits: short imperative subject, body says why. No force-pushes to main.
