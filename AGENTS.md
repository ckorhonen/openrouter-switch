# OpenRouter Switch development guide

Read `README.md`, `TESTING.md`, `SECURITY.md` and `config/schema.md` for routing changes. `gateway/` is the Go CLI/gateway module; `mac/OpenRouterSwitch/` is the Swift package; `scripts/` owns build/check/release tooling; `tests/fresh-install/` owns container lifecycle checks.

Use Go 1.26 from `gateway/go.mod`; macOS with Xcode command-line tools and Swift 5.9+ is needed for the menubar app. From the root, `scripts/build.sh gateway` builds the CLI and `scripts/build-menubar.sh` builds the app. Focused checks are `go test ./...` within `gateway/` and `swift test` within `mac/OpenRouterSwitch/`. CI also checks formatting, vet, shell release contracts and vulnerabilities.

`TESTING.md` requires `scripts/check.sh` for change sets; use `scripts/check.sh --offline` for the hermetic gate when live provider access is outside scope. It builds, vets, tests and starts isolated scratch instances; inspect the current script for exact steps. Offline skips network/credential-dependent checks, including the downloaded secret scanner. For source-only instruction work, record that the runtime gate was not run rather than claiming full acceptance.

Preserve Keychain-before-environment credential precedence, account-scoped model eligibility, explicit-model behavior and protocol-specific fallback. Never persist keys, prompt/response bodies, raw upstream errors or reversible credential fingerprints. Tests must use the isolation seams in `TESTING.md`; never use real Claude settings/config or restart a production gateway. `setup`, `up --install`, client toggles and keyed fresh-install tests affect host/provider state and require that scope to be authorized.

## Completing work

Follow the nearest repository instructions and existing patterns; preserve unrelated edits. Make routine reversible choices within the request and continue through implementation, relevant checks, and repair of failures caused by the change. Ask only for material product decisions, missing prerequisites, or actions outside the authorization. Deployment, publishing, credentials, destructive operations, and live external effects need authorization for that scope.

Choose checks for the affected behavior and existing required gates; do not broaden into unrelated cleanup. For instruction-only edits, inspect source references and run `git diff --check -- AGENTS.md` (include any other changed instruction paths). Report changed paths, actual check results, and unverified runtime behavior. If blocked, give the exact failed command or missing prerequisite, separate baseline failures, and continue independent authorized work.
