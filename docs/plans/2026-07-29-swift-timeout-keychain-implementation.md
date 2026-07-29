# Swift Gateway Timeout and Keychain Update Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Add endpoint-appropriate Swift gateway timeouts and make Keychain ACL attributes add-only.

**Architecture:** `GatewayAPIClient` assigns a timeout to each request, with narrow overrides for model catalog and credential reload. `SecurityOpenRouterCredentialBackend` builds update and add dictionaries through pure helpers so the update path changes only secret data while the add path installs the shared ACL.

**Tech Stack:** Swift, Foundation `URLSession`, Security framework, XCTest

---

### Task 1: Test request timeout policy

**Files:**
- Modify: `mac/OpenRouterSwitch/Tests/OpenRouterSwitchTests/ModelCatalogTests.swift`

1. Extend the existing `URLProtocol` fixture to capture `timeoutInterval`.
2. Add a test for the status, credential reload, and model catalog endpoints.
3. Run the test and verify the endpoint expectations fail before implementation.

### Task 2: Implement request timeout policy

**Files:**
- Modify: `mac/OpenRouterSwitch/Sources/OpenRouterSwitch/GatewayClient.swift`

1. Add 2-second default, 7-second reload, and 22-second catalog constants.
2. Build explicit `URLRequest` values for GET and POST helpers.
3. Pass endpoint overrides from `fetchModelCatalog` and `reloadCredentials`.
4. Keep the session resource timeout above 22 seconds.
5. Run the focused timeout test.

### Task 3: Test Keychain attribute separation

**Files:**
- Modify: `mac/OpenRouterSwitch/Tests/OpenRouterSwitchTests/OpenRouterCredentialStoreTests.swift`

1. Assert update attributes contain only replacement data.
2. Assert add attributes contain replacement data and `kSecAttrAccess`.
3. Run the tests and verify they fail before implementation.

### Task 4: Implement add-only ACL behavior

**Files:**
- Modify: `mac/OpenRouterSwitch/Sources/OpenRouterSwitch/OpenRouterCredentialStore.swift`

1. Attempt `SecItemUpdate` before creating shared access.
2. Pass only `kSecValueData` to `SecItemUpdate`.
3. On `errSecItemNotFound`, create shared access and attach it to `SecItemAdd`.
4. Run focused credential tests.

### Task 5: Validate

**Files:**
- Test: `mac/OpenRouterSwitch/Tests/OpenRouterSwitchTests`

1. Run both focused test classes.
2. Run the complete Swift suite with the full Xcode developer directory.
3. Report outcomes and warnings.
