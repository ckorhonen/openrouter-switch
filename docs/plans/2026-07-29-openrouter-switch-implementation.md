# OpenRouter Switch Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Replace Baseten Switch with an OpenRouter-only product that securely accepts an API key, offers only eligible account models, and supports Claude Code and Codex end to end.

**Architecture:** Preserve the loopback door, router, administration API, macOS menu bar app, native fallbacks, and metadata-only telemetry. Rename provider-specific concepts directly to OpenRouter. Use macOS Keychain first, `OPENROUTER_API_KEY` second, and authenticated `/api/v1/models/user` data as the only model-selection authority.

**Tech Stack:** Go 1.26, Swift 5.9/AppKit/SwiftUI/Security.framework, YAML, shell, GitHub Actions, Nix.

---

### Task 1: Establish the OpenRouter product identity

**Files:**

- Move: `gateway/cmd/baseten-switch/` → `gateway/cmd/openrouter-switch/`
- Move: `mac/BasetenSwitch/` → `mac/OpenRouterSwitch/`
- Move: `mac/OpenRouterSwitch/Sources/BasetenSwitch/` → `mac/OpenRouterSwitch/Sources/OpenRouterSwitch/`
- Move: `mac/OpenRouterSwitch/Tests/BasetenSwitchTests/` → `mac/OpenRouterSwitch/Tests/OpenRouterSwitchTests/`
- Modify: `gateway/go.mod`
- Modify: every Go import containing `github.com/basetenlabs/baseten-switch`
- Modify: `mac/OpenRouterSwitch/Package.swift`
- Modify: `.gitignore`

**Step 1: Add an identity contract test**

Extend `gateway/cmd/openrouter-switch/main_test.go` to assert:

```go
if got := productName(); got != "OpenRouter Switch" {
	t.Fatalf("productName() = %q", got)
}
if got := executableName(); got != "openrouter-switch" {
	t.Fatalf("executableName() = %q", got)
}
```

Update `scripts/tests/test_port_contract.sh` to fail when the built binary is
not `openrouter-switch`.

**Step 2: Run the focused tests and verify failure**

Run:

```sh
cd gateway
go test ./cmd/openrouter-switch
```

Expected: fail until the moved command and renamed identity compile.

**Step 3: Move directories and mechanically rename identity seams**

Apply these exact identity changes:

```text
github.com/basetenlabs/baseten-switch/gateway
  -> github.com/ckorhonen/openrouter-switch/gateway
baseten-switch -> openrouter-switch
BasetenSwitch -> OpenRouterSwitch
Baseten Switch -> OpenRouter Switch
BASETEN_SWITCH_ -> OPENROUTER_SWITCH_
~/.config/baseten-switch -> ~/.config/openrouter-switch
```

Do not yet treat a mechanical `Baseten` → `OpenRouter` rename as semantic
completion. Later tasks delete or rewrite provider-specific behavior.

**Step 4: Format and run compile-level tests**

Run:

```sh
cd gateway
fd -e go . -x gofmt -w
go test ./cmd/openrouter-switch ./internal/config ./internal/door ./internal/launchd
cd ../mac/OpenRouterSwitch
swift test
```

Expected: pass.

**Step 5: Commit**

```sh
git add -A
git commit -m "Rename product to OpenRouter Switch"
```

### Task 2: Replace Baseten OAuth with Keychain-first OpenRouter authentication

**Files:**

- Rewrite: `gateway/internal/auth/store.go`
- Rewrite: `gateway/internal/auth/store_test.go`
- Delete: `gateway/internal/auth/oauth.go`
- Delete: `gateway/internal/auth/oauth_test.go`
- Delete: `gateway/internal/auth/transport.go`
- Delete: `gateway/internal/auth/transport_test.go`
- Modify: `gateway/go.mod`
- Modify: `gateway/go.sum`
- Rewrite: `gateway/cmd/openrouter-switch/auth.go`
- Replace: `gateway/cmd/openrouter-switch/auth_login.go` → `gateway/cmd/openrouter-switch/auth_set_key.go`
- Replace: `gateway/cmd/openrouter-switch/auth_login_test.go` → `gateway/cmd/openrouter-switch/auth_set_key_test.go`
- Modify: `gateway/cmd/openrouter-switch/auth_test.go`
- Modify: `gateway/cmd/openrouter-switch/setup.go`
- Modify: `gateway/cmd/openrouter-switch/setup_test.go`

**Step 1: Write failing credential-store tests**

Cover this precedence and activation contract:

```go
func TestResolveAPIKeyPrefersKeychain(t *testing.T) {
	t.Setenv("OPENROUTER_API_KEY", "env-key")
	store := fakeKeyring{value: "keychain-key"}
	got, source, err := ResolveAPIKey(store)
	if err != nil || got != "keychain-key" || source != SourceKeychain {
		t.Fatalf("ResolveAPIKey() = %q, %q, %v", got, source, err)
	}
}

func TestResolveAPIKeyFallsBackToEnvironment(t *testing.T) {
	t.Setenv("OPENROUTER_API_KEY", "env-key")
	got, source, err := ResolveAPIKey(fakeKeyring{err: ErrKeyNotFound})
	if err != nil || got != "env-key" || source != SourceEnvironment {
		t.Fatalf("ResolveAPIKey() = %q, %q, %v", got, source, err)
	}
}
```

Also test missing credentials, Keychain read failure, store/delete, fingerprint
stability, and errors that never contain the key.

**Step 2: Run tests and verify failure**

Run:

```sh
cd gateway
go test ./internal/auth
```

Expected: fail because the OpenRouter credential API does not exist.

**Step 3: Implement the credential store**

Implement:

```go
const (
	keyringService = "openrouter-switch"
	keyringAccount = "openrouter"
	envAPIKey      = "OPENROUTER_API_KEY"
)

type Source string

const (
	SourceKeychain    Source = "keychain"
	SourceEnvironment Source = "environment"
)
```

Expose `ResolveAPIKey`, `StoreAPIKey`, `DeleteAPIKey`, and
`CredentialFingerprint`. Keychain absence falls through to the environment;
other Keychain errors are reported without revealing secrets.

**Step 4: Implement `/key` validation and CLI commands**

Add an OpenRouter client helper that calls `GET /api/v1/key` with Bearer auth
and returns masked key metadata, `limit`, `limit_remaining`, reset interval,
free-tier state, and expiry.

`openrouter-switch auth set-key`:

- reads a non-echoing terminal prompt;
- validates before Keychain write;
- preserves the prior key on validation failure;
- reloads a running router after success.

`openrouter-switch auth status` reports source and masked metadata. It never
prints the key.

**Step 5: Remove OAuth dependencies and run tests**

Run:

```sh
cd gateway
go mod tidy
gofmt -w internal/auth cmd/openrouter-switch
go test ./internal/auth ./cmd/openrouter-switch
```

Expected: pass and `go list -m all` contains no `golang.org/x/oauth2`.

**Step 6: Commit**

```sh
git add -A
git commit -m "Add secure OpenRouter API key authentication"
```

### Task 3: Rename the routed provider and enforce Bearer transport

**Files:**

- Modify: `gateway/internal/route/route.go`
- Modify: `gateway/internal/route/route_test.go`
- Rewrite: `gateway/internal/proxy/proxy.go`
- Modify: `gateway/internal/proxy/proxy_test.go`
- Modify: `gateway/internal/config/config.go`
- Modify: `gateway/internal/config/config_strict_test.go`
- Modify: `gateway/internal/config/preview.go`
- Modify: `gateway/internal/config/preview_test.go`
- Modify: `gateway/internal/config/gateway.example.yaml`
- Modify: `config/gateway.example.yaml`
- Modify: `gateway/cmd/gateway/config_validation.go`
- Modify: provider-related tests under `gateway/cmd/gateway/`

**Step 1: Write failing route and header tests**

Assert that `route.Routes` contains `openrouter` and not `baseten`.

Assert:

```go
headers, err := BuildUpstreamHeaders(in, UpstreamModeOpenRouter, "sk-or-test")
if err != nil {
	t.Fatal(err)
}
if got := headers.Get("Authorization"); got != "Bearer sk-or-test" {
	t.Fatalf("Authorization = %q", got)
}
```

Also assert that incoming authorization and `X-Api-Key` values are stripped,
an empty OpenRouter key is a hard error, and passthrough native routes preserve
their original credentials.

**Step 2: Run tests and verify failure**

Run:

```sh
cd gateway
go test ./internal/route ./internal/proxy ./internal/config
```

Expected: fail on old route and `Api-Key` header behavior.

**Step 3: Implement route, config, and transport semantics**

- Replace provider/route `baseten` with `openrouter`.
- Permit only `openrouter` in `model_options`.
- Replace `global.auth.baseten` with `global.auth.openrouter`.
- Use `${OPENROUTER_API_KEY}` in examples.
- Collapse Baseten OAuth/API-key proxy modes into one OpenRouter Bearer mode.

**Step 4: Run focused tests**

Run:

```sh
cd gateway
gofmt -w internal/route internal/proxy internal/config cmd/gateway
go test ./internal/route ./internal/proxy ./internal/config ./cmd/gateway
```

Expected: pass.

**Step 5: Commit**

```sh
git add -A
git commit -m "Route gateway traffic through OpenRouter"
```

### Task 4: Build the account-scoped OpenRouter catalog

**Files:**

- Rewrite: `gateway/cmd/gateway/admin_model_catalog.go`
- Rewrite: `gateway/cmd/gateway/admin_model_catalog_test.go`
- Rewrite: `gateway/cmd/gateway/pricing_refresh.go`
- Modify: `gateway/cmd/gateway/pricing_refresh_test.go`
- Modify: `gateway/cmd/gateway/public_catalog_refresh.go`
- Modify: `gateway/cmd/gateway/public_catalog_refresh_test.go`
- Modify: `gateway/cmd/gateway/catalog_models_discovery_test.go`
- Modify: `gateway/cmd/gateway/admin_catalog_health_test.go`
- Modify: `gateway/internal/pricing/catalog.go`
- Modify: `gateway/internal/pricing/pricing.go`
- Modify: `gateway/internal/pricing/cache.go`
- Modify: `gateway/internal/pricing/modelsdev.go`
- Modify: `gateway/internal/pricing/availability.go`
- Modify: pricing tests beside those files
- Delete: `gateway/internal/pricing/baseten_fallback_prices.json`
- Delete: `gateway/internal/pricing/baseten_reasoning_fallback.json`
- Modify: `gateway/internal/modelmeta/modelmeta.go`
- Modify: `gateway/internal/modelmeta/modelmeta_test.go`

**Step 1: Add realistic `/models/user` fixtures and failing decoder tests**

The fixture must include:

- two tool-capable models;
- one model without `tools`;
- prices encoded as strings;
- context and maximum completion lengths;
- text/image modalities;
- supported parameters;
- reasoning metadata and supported efforts.

Assert that decoding retains these fields and rejects duplicate or empty IDs,
oversized responses, non-success status, and trailing JSON.

**Step 2: Add failing key-scope and eligibility tests**

Prove:

- catalog URL is `/api/v1/models/user`;
- Bearer auth is present;
- changing the credential fingerprint invalidates selectable rows;
- a cached catalog from fingerprint A is display-only under fingerprint B;
- only `supported_parameters` containing `tools` are eligible for coding-agent
  selection.

**Step 3: Run tests and verify failure**

Run:

```sh
cd gateway
go test ./internal/pricing ./internal/modelmeta ./cmd/gateway \
  -run 'Catalog|Models|Eligibility|Credential'
```

Expected: fail against Baseten parsers and cache semantics.

**Step 4: Implement the normalized OpenRouter catalog**

Use one authenticated `/models/user` response as the authority for account
availability and OpenRouter pricing. Add `ProviderOpenRouter`. Remove embedded
Baseten catalogs and the Baseten `models.dev` provider slice. Public
`models.dev` data may enrich native Anthropic/OpenAI comparison metadata, but
must never authorize an OpenRouter selection.

**Step 5: Run focused tests**

Run:

```sh
cd gateway
gofmt -w internal/pricing internal/modelmeta cmd/gateway
go test ./internal/pricing ./internal/modelmeta ./cmd/gateway
```

Expected: pass.

**Step 6: Commit**

```sh
git add -A
git commit -m "Add account-scoped OpenRouter model catalog"
```

### Task 5: Replace gateway authentication state and error handling

**Files:**

- Modify: `gateway/cmd/gateway/gateway.go`
- Rewrite: `gateway/cmd/gateway/auth_admin.go`
- Modify: `gateway/cmd/gateway/auth_health_test.go`
- Delete or replace: `gateway/cmd/gateway/auth_tick_test.go`
- Modify: `gateway/cmd/gateway/admin.go`
- Modify: `gateway/cmd/gateway/preflight.go`
- Modify: `gateway/cmd/gateway/preflight_test.go`
- Modify: `gateway/cmd/gateway/gateway_test.go`
- Modify: `gateway/cmd/gateway/telemetry_v1_capture.go`
- Modify: `gateway/cmd/gateway/telemetry_v1_capture_test.go`
- Modify: `gateway/internal/telemetry/event_v1.go`
- Modify: `gateway/internal/telemetry/event_v1_test.go`

**Step 1: Write failing auth-state and error-policy tests**

Cover:

- base URL `https://openrouter.ai/api`;
- missing, valid, invalid, and environment-fallback credential states;
- `401` marks the credential invalid;
- `402` and `403` never activate native fallback;
- `429`, `502`, and `503` honor bounded `Retry-After` before eligible fallback;
- raw upstream error bodies and credentials never enter logs, admin JSON, or
  telemetry.

**Step 2: Run tests and verify failure**

Run:

```sh
cd gateway
go test ./cmd/gateway ./internal/telemetry \
  -run 'Auth|Unauthorized|Payment|Forbidden|RetryAfter|Secret'
```

Expected: fail against OAuth state and old provider naming.

**Step 3: Implement the simpler static-key state machine**

Replace refresh-token generations and OAuth ticks with:

- active credential source and fingerprint;
- last `/key` validation result;
- invalidation on `401`;
- reload after Keychain changes;
- catalog invalidation when the fingerprint changes.

The admin auth response exposes status, source, masked label, limit metadata,
and timestamps only.

**Step 4: Implement status-specific failure behavior**

Keep explicit model choices non-fallback. Preserve existing fallback only for
eligible transient failures. Parse and cap `Retry-After`; do not sleep beyond
the request budget.

**Step 5: Run focused tests**

Run:

```sh
cd gateway
gofmt -w cmd/gateway internal/telemetry
go test ./cmd/gateway ./internal/telemetry
```

Expected: pass.

**Step 6: Commit**

```sh
git add -A
git commit -m "Add OpenRouter gateway auth and failure policy"
```

### Task 6: Enforce eligible models across the gateway and CLI

**Files:**

- Modify: `gateway/cmd/gateway/gateway.go`
- Modify: `gateway/cmd/gateway/models_discovery_test.go`
- Modify: `gateway/cmd/gateway/model_routes_test.go`
- Modify: `gateway/cmd/gateway/subagent_test.go`
- Modify: `gateway/cmd/gateway/admin_reasoning_preflight.go`
- Modify: `gateway/cmd/gateway/admin_reasoning_test.go`
- Modify: `gateway/cmd/openrouter-switch/claude_adapter.go`
- Modify: `gateway/cmd/openrouter-switch/claude_adapter_test.go`
- Modify: `gateway/cmd/openrouter-switch/claude_route_test.go`
- Modify: `gateway/cmd/openrouter-switch/claude_subagents_test.go`
- Modify: `gateway/cmd/openrouter-switch/codex_adapter.go`
- Modify: `gateway/cmd/openrouter-switch/codex_adapter_test.go`
- Modify: adapter safety tests

**Step 1: Write failing selection tests**

Prove:

- arbitrary slash-containing slugs are rejected when absent from the active
  eligible catalog;
- configured but unavailable models are reported, not selected;
- Claude discovery emits stable `claude-openrouter-*` aliases only for eligible
  models;
- alias requests resolve back to the exact OpenRouter slug;
- Codex and Claude route mutation commands reject ineligible targets;
- no catalog means no new selection, while current config remains preserved.

**Step 2: Run tests and verify failure**

Run:

```sh
cd gateway
go test ./cmd/gateway ./cmd/openrouter-switch \
  -run 'Eligible|Unavailable|Alias|Route|Subagent|Catalog'
```

Expected: fail because raw slugs and static aliases are still accepted.

**Step 3: Implement membership gates and dynamic aliases**

Use the same immutable eligible-catalog snapshot for:

- app/admin model lists;
- Claude `/v1/models`;
- explicit request resolution;
- Claude family/subagent mutations;
- Codex route mutations;
- reasoning option mutations.

Saved unavailable values remain in YAML and admin status but cannot be newly
activated.

**Step 4: Rename managed harness profiles**

- Claude alias namespace: `claude-openrouter-*`.
- Codex overlay: `openrouter.config.toml`.
- Codex provider/profile/table: `openrouter`.
- Compatibility sentinel: `openrouter-switch-compat-v1`.
- Remove Baseten-only request-stripping workarounds unless OpenRouter contract
  tests demonstrate a need.

**Step 5: Run focused tests**

Run:

```sh
cd gateway
gofmt -w cmd/gateway cmd/openrouter-switch
go test ./cmd/gateway ./cmd/openrouter-switch
```

Expected: pass.

**Step 6: Commit**

```sh
git add -A
git commit -m "Restrict routes to eligible OpenRouter models"
```

### Task 7: Replace operational CLI, paths, launch agents, and diagnostics

**Files:**

- Modify: remaining files under `gateway/cmd/openrouter-switch/`
- Modify: `gateway/internal/launchd/launchd.go`
- Modify: `gateway/internal/launchd/launchd_test.go`
- Modify: `gateway/internal/pidfile/pidfile.go`
- Modify: `gateway/internal/pidfile/pidfile_test.go`
- Modify: `gateway/internal/doorcli/doorcli.go`
- Modify: `gateway/internal/doorcli/doorcli_test.go`
- Modify: `gateway/internal/config/inittemplate.go`
- Modify: `gateway/internal/config/inittemplate_test.go`

**Step 1: Update failing CLI contract tests**

Require:

```text
openrouter-switch setup
openrouter-switch auth set-key
openrouter-switch auth status
openrouter-switch up --install
openrouter-switch claude on
openrouter-switch codex on
openrouter-switch doctor --probe
```

Assert `setup` has no Baseten CLI lookup or OAuth login.

**Step 2: Run tests and verify failure**

Run:

```sh
cd gateway
go test ./cmd/openrouter-switch ./internal/launchd ./internal/pidfile ./internal/doorcli
```

Expected: fail on old operational paths and labels.

**Step 3: Implement operational identity**

Use:

- config root `~/.config/openrouter-switch`;
- launch labels `com.ckorhonen.openrouter-switch.router` and `.door`;
- app path `~/Applications/OpenRouter Switch.app`;
- `OPENROUTER_SWITCH_*` isolation seams;
- OpenRouter-specific doctor fixes and probes.

Remove all Baseten binary discovery and CLI dependency messages.

**Step 4: Run tests**

Run:

```sh
cd gateway
gofmt -w cmd/openrouter-switch internal/launchd internal/pidfile internal/doorcli internal/config
go test ./cmd/openrouter-switch ./internal/launchd ./internal/pidfile ./internal/doorcli ./internal/config
```

Expected: pass.

**Step 5: Commit**

```sh
git add -A
git commit -m "Update OpenRouter Switch lifecycle and diagnostics"
```

### Task 8: Rename and rebrand the macOS application

**Files:**

- Modify: every Swift file under `mac/OpenRouterSwitch/Sources/OpenRouterSwitch/`
- Modify: every test under `mac/OpenRouterSwitch/Tests/OpenRouterSwitchTests/`
- Modify: `mac/OpenRouterSwitch/Package.swift`
- Replace: `mac/OpenRouterSwitch/Assets/AppIcon.svg`
- Replace: `mac/OpenRouterSwitch/Assets/AppIcon.icns`
- Delete: `mac/OpenRouterSwitch/Assets/baseten-logo.svg`
- Delete: `mac/OpenRouterSwitch/Assets/baseten-logo-white.svg`
- Add: `mac/OpenRouterSwitch/Assets/openrouter-logo.svg`

**Step 1: Update identity and display tests**

Update tests to require:

- module and state names `OpenRouterSwitch`;
- display name `OpenRouter Switch`;
- bundle IDs `com.ckorhonen.openrouter-switch` and
  `com.ckorhonen.openrouter-switch.preview`;
- config and executable paths under OpenRouter names;
- route/provider `openrouter`;
- no Baseten provider labels or colors.

**Step 2: Run Swift tests and verify failure**

Run:

```sh
cd mac/OpenRouterSwitch
swift test
```

Expected: fail until the renamed Swift identity compiles.

**Step 3: Implement Swift identity and analytics renames**

Rename types, environment seams, autosave names, commands, model/provider
fields, traffic labels, fixture keys, accessibility identifiers, and UI text.
Keep native OpenAI branding where it describes the native fallback.

**Step 4: Replace assets**

Create original OpenRouter Switch icon/logo assets. Do not copy proprietary
OpenRouter artwork unless its license explicitly permits redistribution.

**Step 5: Run Swift tests**

Run:

```sh
cd mac/OpenRouterSwitch
swift test
```

Expected: pass.

**Step 6: Commit**

```sh
git add -A
git commit -m "Rebrand the macOS app as OpenRouter Switch"
```

### Task 9: Add secure macOS key management and account-filtered UI

**Files:**

- Add: `mac/OpenRouterSwitch/Sources/OpenRouterSwitch/OpenRouterCredentialStore.swift`
- Add: `mac/OpenRouterSwitch/Tests/OpenRouterSwitchTests/OpenRouterCredentialStoreTests.swift`
- Modify: `mac/OpenRouterSwitch/Sources/OpenRouterSwitch/GatewayClient.swift`
- Modify: `mac/OpenRouterSwitch/Sources/OpenRouterSwitch/OpenRouterSwitchState.swift`
- Modify: `mac/OpenRouterSwitch/Sources/OpenRouterSwitch/RouterWindow.swift`
- Modify: `mac/OpenRouterSwitch/Sources/OpenRouterSwitch/StatusItemController.swift`
- Modify: `mac/OpenRouterSwitch/Sources/OpenRouterSwitch/PopupDisplay.swift`
- Modify: `mac/OpenRouterSwitch/Sources/OpenRouterSwitch/Display.swift`
- Modify: `mac/OpenRouterSwitch/Sources/OpenRouterSwitch/RuntimeCoordination.swift`
- Modify: corresponding Swift tests and fixtures

**Step 1: Write failing credential-store tests**

Use an injected in-memory store. Prove read/write/delete, Keychain precedence,
environment read-only fallback, and errors without key text. Never touch the
developer's Keychain from tests.

**Step 2: Write failing UI/model tests**

Prove:

- the app never reveals an existing key;
- Save validates before replacement;
- Delete removes only the Keychain entry;
- environment fallback is labeled read-only;
- credential changes invalidate outstanding catalog loads;
- only live eligible rows are selectable;
- saved missing models render as unavailable.

**Step 3: Run targeted tests and verify failure**

Run:

```sh
cd mac/OpenRouterSwitch
swift test --filter OpenRouterCredentialStoreTests
swift test --filter ModelCatalogTests
swift test --filter RuntimeCoordinationTests
swift test --filter DisplayTests
```

Expected: fail until the store and UI state exist.

**Step 4: Implement Keychain and UI flow**

Use Security.framework generic-password APIs with service
`openrouter-switch`, account `openrouter`. After a successful write/delete,
ask the gateway to reload credentials and refresh auth/catalog state. The key
never enters CLI arguments, logs, admin responses, or persisted Swift state.

**Step 5: Run the Swift suite**

Run:

```sh
cd mac/OpenRouterSwitch
swift test
```

Expected: pass.

**Step 6: Commit**

```sh
git add -A
git commit -m "Add Keychain auth and account model UI"
```

### Task 10: Update packaging, installation, CI, configuration, and documentation

**Files:**

- Modify: `scripts/build.sh`
- Modify: `scripts/build-menubar.sh`
- Modify: `scripts/check.sh`
- Modify: `scripts/tests/test_port_contract.sh`
- Modify: every file under `scripts/release/`
- Modify: every file under `tests/fresh-install/`
- Modify: `.github/workflows/ci.yml`
- Modify: `.github/workflows/flake.yml`
- Modify: `.github/workflows/release.yml`
- Modify: `.github/ISSUE_TEMPLATE/*.yml`
- Modify: `flake.nix`
- Modify: `README.md`
- Modify: `gateway/README.md`
- Modify: `config/schema.md`
- Modify: `CONTRIBUTING.md`
- Modify: `SECURITY.md`
- Modify: `TESTING.md`
- Modify: `THIRD_PARTY_NOTICES.md`
- Modify: `scripts/release/INSTALL.md`
- Preserve: `LICENSE`

**Step 1: Update release contract tests first**

Require artifact, executable, app, formula, launch label, repository URL, and
install paths under OpenRouter Switch names. Assert the Homebrew formula has no
Baseten CLI dependency.

**Step 2: Run release tests and verify failure**

Run:

```sh
scripts/tests/test_port_contract.sh
scripts/release/test-release-contract.sh
scripts/release/test-release-workflow.sh
scripts/release/test-render-formula.sh
```

Expected: fail against old packaging.

**Step 3: Update build/release/install surfaces**

Rename artifacts, binaries, app bundles, formula metadata, Nix package, launch
assets, paths, and environment seams. Replace the live Baseten OAuth smoke in
`scripts/check.sh` with OpenRouter key status/catalog smokes that never print
the key.

**Step 4: Rewrite public documentation**

Document:

- obtaining and entering an OpenRouter key;
- Keychain/environment precedence;
- account-filtered models;
- Claude Code and Codex setup;
- native fallback behavior;
- privacy and secret handling;
- source build, release, upgrade, uninstall, and migration from Baseten Switch.

Retain MIT attribution in `LICENSE`. Remove obsolete OAuth notice entries.

**Step 5: Run packaging tests**

Run:

```sh
scripts/tests/test_port_contract.sh
scripts/release/test-release-contract.sh
scripts/release/test-release-workflow.sh
scripts/release/test-render-formula.sh
scripts/check.sh --offline
```

Expected: pass.

**Step 6: Commit**

```sh
git add -A
git commit -m "Update OpenRouter packaging and documentation"
```

### Task 11: Complete automated and live end-to-end validation

**Files:**

- Add or modify only tests/fixtures required by discovered failures.
- Do not weaken assertions to obtain green results.

**Step 1: Run formatting, dependency, and static checks**

Run:

```sh
cd gateway
go mod tidy
fd -e go . -x gofmt -w
go build ./...
go vet ./...
go test ./...
cd ../mac/OpenRouterSwitch
swift test
```

Expected: pass.

**Step 2: Run repository gates**

Run:

```sh
scripts/tests/test_port_contract.sh
scripts/release/test-release-contract.sh
scripts/release/test-release-workflow.sh
scripts/release/test-render-formula.sh
scripts/check.sh --offline
scripts/security/run-gitleaks.sh .
```

Expected: pass.

**Step 3: Audit provider residue**

Run:

```sh
rg -n -i 'baseten|basetenlabs|BASETEN_SWITCH|co\.baseten' \
  --hidden --glob '!.git/**' .
```

Expected: only approved historical attribution in `LICENSE`, design/plan
history, or explicit migration documentation. Every runtime, test fixture,
package, command, endpoint, and current-product reference must be gone.

**Step 4: Validate a real key without exposing it**

Use the existing Keychain or `OPENROUTER_API_KEY` only through process
inheritance. Run:

```sh
openrouter-switch auth status
openrouter-switch doctor --probe
scripts/check.sh
```

Expected: valid masked key status, authenticated account catalog, and passing
full gate. Capture no environment dump or shell tracing.

**Step 5: Run Claude Code E2E**

Use the installed Claude Code executable through the managed local profile.
Validate:

- catalog contains only eligible account models;
- streaming response;
- one real tool call;
- reasoning-capable model;
- model switch takes effect;
- unavailable model is rejected locally.

Inspect logs and telemetry for metadata-only output.

**Step 6: Run Codex E2E**

Use the managed `openrouter` Codex profile. Validate:

- streaming Responses request;
- one real tool call;
- model switch takes effect;
- unavailable model is rejected locally.

Inspect logs and telemetry for metadata-only output.

**Step 7: Use Claude Code as an independent closeout reviewer**

Ask local Claude Code to review the branch diff against:

```text
docs/plans/2026-07-29-openrouter-switch-design.md
docs/plans/2026-07-29-openrouter-switch-implementation.md
```

Require concrete file/line findings. Fix every valid blocker and rerun the
affected tests.

**Step 8: Commit validation fixes**

```sh
git add -A
git commit -m "Complete OpenRouter Switch validation"
```

Skip the commit only when the validation pass creates no changes.

### Task 12: Publish and audit completion

**Files:**

- No planned source changes. Fix any audit failures before publication.

**Step 1: Inspect branch scope**

Run:

```sh
git status --short --branch
git log --oneline origin/main..HEAD
git diff --stat origin/main...HEAD
```

Expected: clean worktree and only OpenRouter Switch changes.

**Step 2: Push the branch**

Run:

```sh
git push -u origin codex/openrouter-rewrite
```

Expected: branch exists in `ckorhonen/openrouter-switch`.

**Step 3: Open a draft pull request**

Target `ckorhonen/openrouter-switch:main`. Include:

- complete provider replacement;
- security model;
- account-selection behavior;
- Claude/Codex E2E results;
- automated validation commands;
- known limitations, if any.

**Step 4: Review CI and fix failures**

Run:

```sh
gh pr checks --watch --fail-fast
```

Inspect failures with the repository's documented `gh run` commands. Fix
branch-related failures and rerun local gates before pushing.

**Step 5: Requirement-by-requirement completion audit**

For every requirement in the design, record authoritative evidence:

- fork exists;
- Baseten runtime support is absent;
- OpenRouter Keychain/env auth works;
- `/models/user` controls all selections;
- Claude Code works;
- Codex works;
- secrets remain absent;
- automated gates and CI pass;
- branch is published.

Do not mark the goal complete while any evidence is missing or indirect.

**Step 6: Merge only after the audit is complete**

When every gate is proven, merge the PR into the fork's `main` and verify the
default branch commit and repository contents.
