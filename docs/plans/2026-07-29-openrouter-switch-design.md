# OpenRouter Switch Design

## Goal

Fork Baseten Switch into an OpenRouter-specific product named OpenRouter
Switch. Remove Baseten runtime support, dependencies, defaults, packaging, and
branding. Preserve the local gateway, macOS app, Claude Code and Codex
integrations, native fallbacks, telemetry, and hot routing controls.

## Product identity

- Repository: `ckorhonen/openrouter-switch`
- Product: `OpenRouter Switch`
- CLI: `openrouter-switch`
- macOS app and Swift module: `OpenRouterSwitch`
- Configuration root: `~/.config/openrouter-switch`
- Configuration file: `~/.config/openrouter-switch/gateway.yaml`
- Environment prefix: `OPENROUTER_SWITCH_`
- OpenRouter API key fallback: `OPENROUTER_API_KEY`
- Primary routed provider and route name: `openrouter`

Historical MIT attribution remains intact. Current runtime behavior,
documentation, examples, release assets, and user-visible text must not retain
Baseten-specific behavior.

## Architecture

Keep the current three-process shape:

1. Coding harnesses send requests to a loopback front door.
2. The door forwards healthy traffic to the router and owns native fallback.
3. The router authenticates to OpenRouter, rewrites selected models, records
   metadata-only telemetry, and exposes a loopback-only administration API.

This is an OpenRouter-specific implementation. Types, configuration fields,
routes, CLI commands, and UI terminology should name OpenRouter directly
instead of introducing a generic provider abstraction.

## Authentication

OpenRouter uses a static API key rather than the Baseten CLI OAuth credential.

Credential precedence:

1. macOS Keychain entry owned by OpenRouter Switch;
2. `OPENROUTER_API_KEY`;
3. no credential.

The Keychain service is `openrouter-switch`; the default account is
`openrouter`. CLI and app writes target the same entry. Key entry uses a secure
input field or non-echoing terminal prompt. The application never writes the
key to `gateway.yaml`.

Before activation, a new key is validated with authenticated
`GET https://openrouter.ai/api/v1/key`. A failed validation leaves the prior
working credential active. The administration API may expose masked key
metadata, authentication state, limit information, and failure class, but
never the key or a reversible derivative.

Every OpenRouter request uses:

```text
Authorization: Bearer <OPENROUTER_API_KEY>
```

The gateway base URL is `https://openrouter.ai/api`; existing route builders
append `/v1/messages`, `/v1/chat/completions`, `/v1/responses`, and model
catalog paths.

## Account-scoped model discovery

OpenRouter Switch fetches:

```text
GET https://openrouter.ai/api/v1/models/user
Authorization: Bearer <key>
```

The response is the selection authority. The normalized catalog records:

- model id and display name;
- prompt, completion, cache, request, image, and other published prices;
- context and maximum output length;
- input and output modalities;
- supported parameters;
- reasoning capabilities and supported effort levels;
- account, privacy, provider-preference, and guardrail-filtered availability.

Selectable models are computed separately for each client:

```text
Claude Code =
  account catalog
  intersect Messages-compatible
  intersect tool-capable

Codex =
  account catalog
  intersect Responses-compatible
  intersect tool-capable
```

Configured models missing from the live catalog remain visible as unavailable
state. They cannot be newly selected and are rejected locally if requested
explicitly. OpenRouter Switch never silently substitutes another model.

Catalog caches are scoped to a non-reversible fingerprint of the active key.
Credential changes invalidate selectable models immediately. A catalog fetched
under one credential is never offered under another. Stale catalog data can
support display and historical pricing, but not new selection.

Claude Code model discovery synthesizes stable `claude-openrouter-*` aliases
for eligible OpenRouter models and maintains the alias-to-slug mapping in the
active catalog snapshot. The static Baseten alias list is removed.

## Request routing

### Claude Code

Claude Code continues to use the local Anthropic-shaped listener. OpenRouter
traffic is sent to `/v1/messages` using the selected OpenRouter slug. Streaming,
tool calls, images, PDFs, and thinking blocks pass through in the OpenRouter
Messages shape. Native fallback continues to target Anthropic with the
harness's original credentials.

### Codex

Codex continues to use a managed local profile and the OpenAI Responses-shaped
listener. OpenRouter traffic is sent to `/v1/responses`. The managed profile,
compatibility sentinel, and request normalizers are renamed and retained only
where current Codex behavior requires them. Baseten-only compatibility rules
are removed after endpoint-specific tests prove they are unnecessary.

Native fallback continues to target OpenAI with the harness's original
credentials.

## Failure behavior

- `401 Unauthorized`: mark the active key invalid and request replacement.
- `402 Payment Required`: report exhausted account or key budget. Do not fall
  back to another paid provider.
- `403 Forbidden`: report guardrail, privacy, or permission rejection. Do not
  fall back.
- `429 Too Many Requests`: apply bounded retry and the existing eligible native
  fallback policy.
- `502 Bad Gateway` and `503 Service Unavailable`: apply bounded retry and the
  existing eligible native fallback policy.
- Catalog unavailable: preserve active configuration, disable new model
  selection, and show the last error without exposing response bodies.
- Keychain unavailable: use the environment fallback only when present.

Retries honor `Retry-After`. Explicit model choices remain non-fallback
requests, matching the existing safety contract.

## Telemetry and privacy

Telemetry remains metadata-only. It may record:

- provider and model;
- endpoint shape;
- latency, time to first token, tokens, and estimated cost;
- retry and fallback outcomes;
- normalized OpenRouter error class.

It must not record request bodies, response bodies, prompt content, response
content, authorization headers, API keys, Keychain data, or raw upstream error
bodies.

Secret-redaction tests cover logs, admin JSON, telemetry, doctor output, and
failed catalog/authentication responses.

## macOS application

The menu bar app is renamed and rebranded as OpenRouter Switch. It adds:

- secure API-key entry and replacement;
- masked key status and remaining configured key limit when available;
- account-filtered model menus;
- unavailable saved-model presentation;
- OpenRouter-specific error messages and links.

Keychain is the default credential store. The environment fallback remains
read-only from the app's perspective.

## CLI and configuration

Primary commands become:

```text
openrouter-switch setup
openrouter-switch auth set-key
openrouter-switch auth status
openrouter-switch up --install
openrouter-switch claude on
openrouter-switch codex on
openrouter-switch doctor --probe
```

The configuration route, auth key, model options provider, status fields, and
telemetry provider become `openrouter`. Baseten CLI discovery, OAuth login,
profile parsing, refresh-token handling, and Baseten API-key fallback flags are
deleted.

There is no automatic migration from `~/.config/baseten-switch`. The fork uses
an isolated configuration root to avoid mutating an installed Baseten Switch.
Documentation provides an explicit model-routing migration example without
copying credentials.

## Packaging and documentation

Rename:

- Go module and imports;
- commands and binaries;
- Swift package, module, app identifiers, launch agents, assets, and menus;
- Homebrew/release formulas and install paths;
- environment variables, config paths, logs, pid files, and backup paths;
- README, schema, testing, security, contribution, and release documentation.

Remove:

- Baseten CLI dependency;
- Baseten OAuth and profile-store integration;
- Baseten API hosts and endpoint contracts;
- Baseten embedded pricing/reasoning catalogs;
- Baseten logos and provider-specific compatibility work;
- Baseten-only tests and fixtures.

Replace removed coverage with OpenRouter-specific fixtures and contract tests.

## Validation

Automated:

- all Go unit and integration tests;
- all Swift tests;
- shell install, release, security, and port-contract tests;
- `scripts/check.sh`;
- tracked-file audit for unintended Baseten runtime references;
- secret-pattern scan.

Live:

- validate a real key without printing it;
- prove `/models/user` controls both app and harness model selection;
- Claude Code streaming, tool call, reasoning, and model switch;
- Codex streaming, tool call, and model switch;
- invalid-key and unavailable-model behavior;
- log and telemetry inspection for secret leakage.

## Release strategy

Work lands on `codex/openrouter-rewrite` in
`ckorhonen/openrouter-switch`. Commits remain narrow enough to review, but the
completion gate is the full OpenRouter-only product. The branch is pushed only
after local validation, then reviewed and audited against every requirement in
this design.
