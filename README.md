# OpenRouter Switch

OpenRouter Switch is a local macOS app and gateway that routes supported AI
coding harnesses between their native providers and models served on Baseten.
One global switch controls Baseten routing, and per-client mappings select the
model that serves each request.

> **Beta:** OpenRouter Switch is under active development. Interfaces,
> configuration, and behavior may change between releases. The current macOS
> build is ad hoc signed and not yet notarized by Apple, so first launch may
> require approval under System Settings → Privacy & Security.

The first public release supports macOS 13 or newer on Apple Silicon and Intel.

## Quick start

```sh
brew install basetenlabs/baseten/openrouter-switch
openrouter-switch setup
openrouter-switch up --install
openrouter-switch claude on
openrouter-switch doctor --probe
```

The fully qualified Homebrew command adds Baseten's public tap and installs
both OpenRouter Switch and its Baseten CLI dependency. It requires no GitHub
login, separate `brew tap`, second install command, or local compiler. The beta
release includes a universal macOS artifact for Apple Silicon and Intel.

`setup` verifies the Baseten CLI and its current credential, opens
`baseten auth login` when needed, and creates the initial configuration.
It never overwrites an existing configuration. `up --install` installs the
user launch agents, starts the local gateway, installs OpenRouter Switch.app in
`~/Applications`, and opens the app. `claude on` connects new Claude Code
sessions to the gateway. The final command checks the complete request path
with a small live request.

If macOS blocks the app's first launch, open **System Settings → Privacy &
Security**, scroll to **Security**, and click **Open Anyway**. This control
appears after a blocked launch attempt. A managed Mac may prohibit the
override.

## Use the Mac app

The menu bar app is the primary interface for daily use. It provides:

- the global Baseten routing switch;
- Claude Code and Codex model configuration;
- current health, active fallback, and authentication status;
- recent traffic, performance, and spend views;
- actions to start the system and run it at login.

After an upgrade, `openrouter-switch up` adopts the new CLI and app version.
Run `openrouter-switch menubar` when you only need to install, refresh, or reopen
the app from the current Homebrew package.

The app and CLI update the same configuration. You can use either interface
without maintaining separate state.

## Claude Code

`openrouter-switch claude on` saves the previous Claude Code setting and points
new sessions at OpenRouter Switch. Restart Claude Code after enabling or
disabling the integration.

Useful controls:

```sh
openrouter-switch on
openrouter-switch off
openrouter-switch claude status
openrouter-switch claude route
openrouter-switch claude route sonnet zai-org/GLM-5.2
openrouter-switch claude route sonnet native
openrouter-switch claude subagents zai-org/GLM-5.2
```

`on` and `off` change one global routing switch. Saved model mappings remain
editable while routing is off. A Claude family can map to `native`, a
configured alias, or a Baseten model slug. Run
`openrouter-switch claude route <family> default` to remove a family override.

To restore the Claude Code setting that existed before setup:

```sh
openrouter-switch claude off
```

## Codex CLI

Codex support is opt-in. Install
[Codex CLI](https://github.com/openai/codex), start OpenRouter Switch,
and create its managed profile:

```sh
openrouter-switch codex on
openrouter-switch codex route zai-org/GLM-5.2
openrouter-switch codex status
codex --profile baseten
```

The first `codex on` may request permission to enable the parked Codex
listener. OpenRouter Switch writes `~/.codex/baseten.config.toml`; it does not
modify `~/.codex/config.toml`. Start Codex without `--profile baseten` to use
native OpenAI routing.

Remove the managed profile and restore any file it replaced:

```sh
openrouter-switch codex off
```

Do not override the managed profile's compatibility model with `-m`. Select
the upstream Baseten model with `openrouter-switch codex route` instead.

## Status and troubleshooting

```sh
openrouter-switch status
openrouter-switch status --verbose
openrouter-switch doctor
openrouter-switch doctor --probe
```

`status` summarizes the router, front door, Mac app, authentication, and
client routing state. `doctor` inspects the same path without changing it and
prints the first failure with a concrete fix. `doctor --fix` can apply
supported repairs after confirmation.

OpenRouter Switch stores configuration, local state, logs, and telemetry under
`~/.config/openrouter-switch/`. The primary logs are:

```text
~/.config/openrouter-switch/logs/router.log
~/.config/openrouter-switch/logs/door.log
```

Run `openrouter-switch auth login` if the Baseten credential expires. The command
delegates authentication to the Baseten CLI, reloads the gateway, and prints
the current identity.

## Upgrade

```sh
brew upgrade openrouter-switch
openrouter-switch up
openrouter-switch doctor
```

`up` leaves healthy current components alone and moves stale components to the
new binary and app. Homebrew remains the canonical public install and upgrade
channel.

## Uninstall

Inspect the exact removal first:

```sh
openrouter-switch uninstall --dry-run
```

Then remove managed harness settings, processes, launch agents, runtime
residue, and the Mac app:

```sh
openrouter-switch uninstall
brew uninstall openrouter-switch
```

The default uninstall retains configuration, telemetry, logs, and backups.
To remove those files as well, use this instead of the standard uninstall:

```sh
openrouter-switch uninstall --purge --yes
brew uninstall openrouter-switch
```

Uninstall never removes Baseten CLI credentials or keychain entries. If the
app's Start at Login item prevents safe bundle removal, the command prints the
manual action required in macOS System Settings.

## Privacy and trust

OpenRouter Switch binds its services to the local loopback interface. It receives
harness requests and credentials because it is in the selected request path.
Request content leaves the machine only for the upstream chosen by the active
routing policy. Baseten credentials go only to Baseten, and native credentials
go only to their matching native provider.

Local telemetry contains request metadata, not prompts, responses,
credentials, headers, or request bodies. Disable future records by setting
`telemetry_enabled: false` in `gateway.yaml`, then reload the configuration.
Delete existing records from
`~/.config/openrouter-switch/telemetry/`.

Public model-catalog refreshes from models.dev send no credential or
user-derived data. The unauthenticated administration API binds to loopback
and must never be exposed on a network interface.

## Configuration

The generated configuration is
`~/.config/openrouter-switch/gateway.yaml`. Prefer the Mac app or typed CLI
commands over direct edits; routing changes hot-reload without a restart.

See [config/schema.md](config/schema.md) for every field and
[config/gateway.example.yaml](config/gateway.example.yaml) for the generated
shape. Store API-key overrides in
`~/.config/openrouter-switch/env`, which must use mode `0600`, rather than in
`gateway.yaml`.

## Build and test

The Go module in `gateway/` builds the CLI, router, administration API, and
front door. The Swift package in `mac/OpenRouterSwitch/` builds the native menu
bar app without external Swift packages.

```sh
scripts/build.sh
scripts/check.sh
```

See [CONTRIBUTING.md](CONTRIBUTING.md) for contribution and dependency rules.
See [TESTING.md](TESTING.md) for test layers and isolation requirements.

## Nix source build

The Nix flake is an alternate source build for Linux and Apple Silicon macOS.
It does not include the signed Mac app or install the Baseten CLI dependency:

```sh
nix profile install github:basetenlabs/openrouter-switch#openrouter-switch
```

Upgrade and restart the locally running components:

```sh
nix profile upgrade --refresh openrouter-switch
openrouter-switch up
```

Homebrew is the supported path for the complete macOS product.

## License

OpenRouter Switch is available under the [MIT License](LICENSE).
