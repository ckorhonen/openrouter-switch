# OpenRouter Switch gateway

This Go module builds the `openrouter-switch` CLI, local router, admin API, and
front door. See the repository [README.md](../README.md) for installation and
[TESTING.md](../TESTING.md) for validation.

## Build

From the repository root:

```sh
scripts/build.sh
```

Or build the CLI directly:

```sh
cd gateway
go build -o bin/openrouter-switch ./cmd/openrouter-switch
```

## Start a development instance

```sh
bin/openrouter-switch config init
bin/openrouter-switch up
bin/openrouter-switch status
```

Use the adapters instead of editing harness configuration by hand:

```sh
bin/openrouter-switch claude on
bin/openrouter-switch codex on
```

The generated configuration lives at
`~/.config/openrouter-switch/gateway.yaml`. Runtime state, logs, and telemetry
use the same configuration directory.

## Main commands

```text
up, down, restart, status
on, off
config init, config reset
claude on|off|status|subagents|route|reasoning
codex on|off|status|route|reasoning
auth set-key, auth status, doctor, spend
gateway start|stop|restart|status
door
```

Run `openrouter-switch <command> --help` for the current flags and environment
overrides.

## Module layout

```text
cmd/openrouter-switch/   CLI and lifecycle commands
cmd/gateway/          router, admin API, and request handling
internal/auth/        OpenRouter Keychain/env credential handling
internal/config/      configuration parsing and editing
internal/door/        front-door configuration
internal/proxy/       upstream request and response relay
internal/telemetry/   segmented request telemetry
internal/translate/   protocol translation
```

## Development checks

From the repository root, run the full gate:

```sh
scripts/check.sh
```

For a quick package-only pass:

```sh
cd gateway
go test ./...
go vet ./...
```

The full gate remains required because it also builds the Swift app and runs
isolated lifecycle and credential-store smoke tests.
