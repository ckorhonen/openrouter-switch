# OpenRouter Switch

OpenRouter Switch is a local macOS app and gateway for routing Claude Code and
Codex through models available to your OpenRouter account. One switch controls
OpenRouter routing; client-specific mappings select models for Claude Code and
Codex while preserving optional native Anthropic and OpenAI fallback.

> **Beta:** Interfaces, configuration, and packaging may change before a
> stable release. Release builds are ad-hoc signed and are not notarized.

OpenRouter Switch is a fork of
[Baseten Switch](https://github.com/basetenlabs/baseten-switch). The upstream
MIT copyright and license are preserved in [LICENSE](LICENSE).

## Install

Homebrew is the canonical install and upgrade path:

```sh
brew install ckorhonen/openrouter-switch/openrouter-switch
```

Then configure a key and start the services:

```sh
openrouter-switch setup
openrouter-switch up --install
openrouter-switch menubar
openrouter-switch claude on
openrouter-switch doctor --probe
```

`setup` prompts for a key when none is configured. `auth set-key` replaces it.
Both prompts avoid terminal echo, validate against OpenRouter, and update
Keychain only after validation succeeds. The prior working key remains active
when validation fails.

Fresh configuration starts with native routing off and no assumed model.
Choose one of the account models offered by the app, then turn OpenRouter
routing on.

## Authentication and account models

Credential precedence:

1. macOS Keychain service `openrouter-switch`, account `openrouter`;
2. inherited `OPENROUTER_API_KEY`;
3. no credential.

Environment credentials are a read-only fallback. Do not put an OpenRouter API
key in `gateway.yaml`, shell history, command arguments, or issue reports.

```sh
openrouter-switch auth status
openrouter-switch auth set-key
```

The gateway validates credentials with OpenRouter's authenticated `/api/v1/key`
endpoint. It obtains selectable models from authenticated
`/api/v1/models/user`. The app and CLI offer only tool-capable models present
in that account-scoped response and compatible with the selected client.
Public model catalogs may enrich display metadata, but never authorize model
selection.

A saved model that disappears from the account catalog remains visible as
unavailable and cannot be selected or requested. OpenRouter Switch never
silently substitutes another OpenRouter model.

## Claude Code

```sh
openrouter-switch claude on
openrouter-switch claude status
openrouter-switch claude route
openrouter-switch claude route sonnet <account-model>
openrouter-switch claude subagents <account-model>
```

Restart Claude Code after enabling or disabling the integration. OpenRouter
models are exposed through the local Anthropic-compatible Messages endpoint.
Streaming, tool calls, multimodal content, and supported reasoning controls are
forwarded in the OpenRouter Messages shape.

Disable the managed integration:

```sh
openrouter-switch claude off
```

## Codex

```sh
openrouter-switch codex route <account-model>
openrouter-switch codex on
openrouter-switch codex status
codex --profile openrouter
```

OpenRouter Switch writes a managed `~/.codex/openrouter.config.toml` overlay. It
does not modify `~/.codex/config.toml`. Start Codex without
`--profile openrouter` to use the normal Codex configuration.

Disable the managed integration:

```sh
openrouter-switch codex off
```

## Routing and fallback

```sh
openrouter-switch on
openrouter-switch off
openrouter-switch status --verbose
```

When routing is off, requests use the client's native provider and OpenRouter
credentials are not consulted. When routing is on, configured requests use
OpenRouter.

Native fallback is optional and protocol-specific: Claude Code falls back only
to Anthropic; Codex falls back only to OpenAI. Explicit model choices never
fall back. Authentication, payment, permission, and guardrail failures
(`401`, `402`, `403`) never fall back. Eligible transient failures such as
`429`, `502`, and `503` may use bounded retry and configured native fallback.

## Files and diagnostics

OpenRouter Switch stores current-product state under:

```text
~/.config/openrouter-switch/
├── gateway.yaml
├── logs/
├── telemetry/
└── backups/
```

Useful commands:

```sh
openrouter-switch status
openrouter-switch doctor
openrouter-switch doctor --probe
```

Configuration can be overridden with
`OPENROUTER_SWITCH_CONFIG_PATH`. See [config/schema.md](config/schema.md) for
the complete schema.

## Privacy and security

The router, door, and administration API bind to loopback. Prompt and response
bodies are forwarded only to the selected provider. Telemetry is local and
metadata-only: provider, model, endpoint shape, latency, token counts, cost
estimate, and normalized retry or fallback outcomes.

OpenRouter Switch must not log or persist:

- API keys or authorization headers;
- prompt or response content;
- raw upstream error bodies;
- Keychain data;
- reversible credential fingerprints.

See [SECURITY.md](SECURITY.md) for private vulnerability reporting.

## Build from source

Requirements:

- Go from `gateway/go.mod`;
- macOS with Xcode command-line tools for the app;
- Swift 5.9 or newer.

```sh
scripts/build.sh gateway
scripts/check.sh --offline
```

The CLI is written to `gateway/bin/openrouter-switch`. Build the app with:

```sh
scripts/build-menubar.sh
```

Nix builds the CLI only:

```sh
nix profile install github:ckorhonen/openrouter-switch#openrouter-switch
```

## Upgrade and uninstall

```sh
brew upgrade openrouter-switch
openrouter-switch up
openrouter-switch doctor
```

Preview uninstall actions:

```sh
openrouter-switch uninstall --dry-run
```

Remove the current installation while retaining configuration and telemetry:

```sh
openrouter-switch uninstall
brew uninstall openrouter-switch
```

Purge current-product data only:

```sh
openrouter-switch uninstall --purge --yes
```

The Keychain entry is retained unless explicitly removed in the app or macOS
Keychain Access.

## Migrating from Baseten Switch

OpenRouter Switch uses an isolated config root and does not automatically read
or modify `~/.config/baseten-switch`. Recreate model routes in
`~/.config/openrouter-switch/gateway.yaml`, replacing each old provider route
with `openrouter`, then enter an OpenRouter key using `auth set-key`. Do not
copy provider credentials or old authentication files.

## License

OpenRouter Switch is available under the [MIT License](LICENSE). Third-party
notices are in [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md).
