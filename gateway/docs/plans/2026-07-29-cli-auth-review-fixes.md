# CLI and Auth Review Fixes Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Fix catalog timeout, doctor forbidden status, auth-map validation, and Keychain-first gateway credential fallback.

**Architecture:** Keep quick mutation polling and slow catalog hydration on separate timeout budgets. Centralize credential authority in `internal/auth` and reject unsupported YAML credential authorities during routing validation.

**Tech Stack:** Go, `net/http`, table-driven tests, `httptest`.

---

### Task 1: Model-catalog timeout

**Files:**
- Modify: `cmd/openrouter-switch/reasoning.go`
- Test: `cmd/openrouter-switch/reasoning_test.go`

1. Add a failing delayed-response eligibility test whose delay exceeds the
   short mutation timeout.
2. Run the focused test and confirm timeout failure.
3. Add a 25-second model-catalog timeout used only by
   `requireEligibleOpenRouterModel`.
4. Run the focused test and confirm pass.

### Task 2: Doctor forbidden status

**Files:**
- Modify: `cmd/openrouter-switch/doctor.go`
- Test: `cmd/openrouter-switch/doctor_test.go`

1. Add a failing `forbidden` doctor subtest.
2. Classify `forbidden` as `docFail`, sanitize detail, and recommend
   `openrouter-switch auth set-key`.
3. Run `TestDoctorAuthHealthCheck`.

### Task 3: Routing auth-map validation

**Files:**
- Modify: `internal/config/config.go`
- Test: `internal/config/config_strict_test.go`

1. Add table tests accepting `anthropic`, rejecting `openrouter` with Keychain
   guidance, and rejecting unknown providers.
2. Sort keys and validate `global.auth` before client validation.
3. Run the focused config tests.

### Task 4: Canonical credential fallback

**Files:**
- Modify: `cmd/gateway/gateway.go`
- Test: `cmd/gateway/catalog_credential_race_test.go`

1. Add a resolver seam test proving the no-configured-resolver path returns a
   Keychain-sourced canonical result despite environment/config values.
2. Delegate the fallback to `auth.ResolveDefaultAPIKey`.
3. Run focused gateway credential tests.

### Task 5: Validation

1. Run `gofmt` on changed Go files.
2. Run focused tests for all four fixes.
3. Run `go test -count=1 -timeout=180s ./...`.
4. Run `go vet ./...` and `git diff --check`.

