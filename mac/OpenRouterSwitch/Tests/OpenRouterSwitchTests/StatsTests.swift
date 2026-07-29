import XCTest
@testable import OpenRouterSwitch

// Decode tests for the /v1/admin/stats contract (StatsSnapshot and
// friends) and the admin-status additions (version, auth). Hermetic:
// payloads are parsed from JSON literals, never fetched.

final class StatsTests: XCTestCase {

    private func decode(_ json: String) -> [String: Any] {
        let obj = try! JSONSerialization.jsonObject(
            with: Data(json.utf8), options: [])
        return obj as! [String: Any]
    }

    // Full payload per the contract: every field lands, buckets and
    // recent keep their oldest-first order.
    func testStatsSnapshotDecodesFullPayload() {
        let json = """
        {
          "window_seconds": 3600, "bucket_seconds": 60,
          "buckets": [
            {"ts": 1783994400, "requests": 12, "errors": 1, "p50_ms": 900, "p95_ms": 4100},
            {"ts": 1783994460, "requests": 0, "errors": 0, "p50_ms": 0, "p95_ms": 0}
          ],
          "fallback_active": {"claude-code": false, "codex": true},
          "recent": [
            {"ts": 1783997990.1, "client": "claude-code", "route": "anthropic",
             "route_effective": "", "requested_model": "claude-fable-5",
             "upstream_model": "claude-fable-5", "status": 200,
             "duration_ms": 812, "subagent": false},
            {"ts": 1783997995.5, "client": "claude-code", "route": "openrouter",
             "route_effective": "openrouter", "requested_model": "claude-opus-4-8",
             "upstream_model": "zai-org/GLM-5.2", "status": 502,
             "duration_ms": 90.5, "subagent": true}
          ]
        }
        """
        let s = StatsSnapshot(dict: decode(json))
        XCTAssertEqual(s.windowSeconds, 3600)
        XCTAssertEqual(s.bucketSeconds, 60)
        XCTAssertEqual(s.buckets.count, 2)
        XCTAssertEqual(s.buckets[0].ts, 1_783_994_400)
        XCTAssertEqual(s.buckets[0].requests, 12)
        XCTAssertEqual(s.buckets[0].errors, 1)
        XCTAssertEqual(s.buckets[0].p50Ms, 900)
        XCTAssertEqual(s.buckets[0].p95Ms, 4100)
        XCTAssertEqual(s.buckets[1].requests, 0)
        XCTAssertEqual(s.fallbackActive, ["claude-code": false, "codex": true])
        XCTAssertEqual(s.recent.count, 2)
        XCTAssertEqual(s.recent[0].client, "claude-code")
        XCTAssertEqual(s.recent[0].route, "anthropic")
        XCTAssertEqual(s.recent[0].routeEffective, "")
        XCTAssertEqual(s.recent[0].requestedModel, "claude-fable-5")
        XCTAssertEqual(s.recent[0].upstreamModel, "claude-fable-5")
        XCTAssertEqual(s.recent[0].status, 200)
        XCTAssertEqual(s.recent[0].durationMs, 812)
        XCTAssertFalse(s.recent[0].subagent)
        XCTAssertEqual(s.recent[1].ts, 1_783_997_995.5, accuracy: 0.001)
        XCTAssertEqual(s.recent[1].upstreamModel, "zai-org/GLM-5.2")
        XCTAssertEqual(s.recent[1].status, 502)
        XCTAssertTrue(s.recent[1].subagent)
    }

    // Absent keys decode to zero values or empty collections, never nil.
    func testStatsSnapshotAbsentKeys() {
        let s = StatsSnapshot(dict: decode(#"{"window_seconds": 600}"#))
        XCTAssertEqual(s.windowSeconds, 600)
        XCTAssertEqual(s.bucketSeconds, 0)
        XCTAssertTrue(s.buckets.isEmpty)
        XCTAssertTrue(s.fallbackActive.isEmpty)
        XCTAssertTrue(s.recent.isEmpty)

        // Entries with missing fields keep zero values, not crashes.
        let partial = StatsSnapshot(dict: decode(
            #"{"buckets": [{"ts": 5}], "recent": [{"client": "codex"}]}"#))
        XCTAssertEqual(partial.buckets[0].ts, 5)
        XCTAssertEqual(partial.buckets[0].requests, 0)
        XCTAssertEqual(partial.recent[0].client, "codex")
        XCTAssertEqual(partial.recent[0].status, 0)
        XCTAssertFalse(partial.recent[0].subagent)
    }

    // Empty payload: fully zeroed snapshot.
    func testStatsSnapshotEmptyPayload() {
        let s = StatsSnapshot(dict: decode("{}"))
        XCTAssertEqual(s.windowSeconds, 0)
        XCTAssertEqual(s.bucketSeconds, 0)
        XCTAssertTrue(s.buckets.isEmpty)
        XCTAssertTrue(s.fallbackActive.isEmpty)
        XCTAssertTrue(s.recent.isEmpty)
    }

    // Malformed nested values are tolerated (wrong types drop to zero
    // values or are skipped), matching the ClientStatus decode style.
    func testStatsSnapshotToleratesMalformedValues() {
        let s = StatsSnapshot(dict: decode("""
        {"window_seconds": "soon", "buckets": "nope",
         "fallback_active": {"claude-code": "yes"}, "recent": [{"ts": "x"}]}
        """))
        XCTAssertEqual(s.windowSeconds, 0)
        XCTAssertTrue(s.buckets.isEmpty)
        XCTAssertEqual(s.fallbackActive, ["claude-code": false])
        XCTAssertEqual(s.recent.count, 1)
        XCTAssertEqual(s.recent[0].ts, 0)
    }

    // MARK: - AdminStatusSnapshot (version + auth additions)

    func testAdminStatusSnapshotDecodes() {
        let snap = AdminStatusSnapshot(dict: decode("""
        {
          "uptime_seconds": 12, "version": "v0.2.1",
          "config_path": "/tmp/live/gateway.yaml",
          "global_routing_enabled": true,
          "auth": {"status": "valid", "source": "keychain",
                   "label": "sk-or-…test",
                   "limit": 100, "limit_remaining": "42.5",
                   "limit_reset": "monthly", "is_free_tier": false,
                   "expires_at": "", "last_ok_at": "2026-07-29T12:00:00Z",
                   "last_error": ""},
          "clients": [{"name": "claude-code", "enabled": true, "effective_route": "openrouter"}]
        }
        """))
        XCTAssertEqual(snap.version, "v0.2.1")
        XCTAssertEqual(snap.configPath, "/tmp/live/gateway.yaml")
        XCTAssertTrue(snap.globalRoutingEnabled)
        XCTAssertEqual(snap.auth?.status, "valid")
        XCTAssertEqual(snap.auth?.source, "keychain")
        XCTAssertEqual(snap.auth?.maskedLabel, "sk-or-…test")
        XCTAssertEqual(snap.auth?.limit, 100)
        XCTAssertEqual(snap.auth?.limitRemaining, 42.5)
        XCTAssertEqual(snap.auth?.limitReset, "monthly")
        XCTAssertEqual(snap.auth?.isFreeTier, false)
        XCTAssertEqual(snap.auth?.validatedAt, "2026-07-29T12:00:00Z")
        XCTAssertTrue(snap.auth?.isValid == true)
        XCTAssertEqual(snap.clients.count, 1)
        XCTAssertEqual(snap.clients[0].name, "claude-code")
    }

    func testAdminStatusSnapshotAbsentKeys() {
        let snap = AdminStatusSnapshot(dict: decode("{}"))
        XCTAssertEqual(snap.version, "")
        XCTAssertEqual(snap.configPath, "")
        XCTAssertNil(snap.auth)
        XCTAssertTrue(snap.clients.isEmpty)
        XCTAssertFalse(snap.globalRoutingEnabled)

        // Auth block with missing fields: zero values, not nil.
        let partial = AdminStatusSnapshot(dict: decode(#"{"auth": {}}"#))
        XCTAssertNotNil(partial.auth)
        XCTAssertEqual(partial.auth?.status, "unknown")
        XCTAssertEqual(partial.auth?.source, "")

        let routedClientWithoutGlobalField = AdminStatusSnapshot(dict: decode("""
        {"clients": [{"name": "claude-code", "enabled": true,
                      "effective_route": "openrouter"}]}
        """))
        XCTAssertFalse(routedClientWithoutGlobalField.globalRoutingEnabled)
    }

    func testAdminStatusSnapshotDecodesGlobalRoutingContract() {
        let snap = AdminStatusSnapshot(dict: decode("""
        {
          "router_boot_id": "boot-a",
          "active_generation": 42,
          "active_config_hash": "sha256:active",
          "desired_config_hash": "sha256:active",
                    "capabilities": ["global_routing"],
          "health": "ready",
          "uptime_seconds": 3600,
          "global_routing_enabled": false,
          "reload": {"state": "applied", "error": ""},
          "clients": [{
            "name": "claude-code",
            "enabled": true,
            "native_route": "anthropic",
            "effective_summary": "Native · Anthropic",
            "subagent_model": "zai-org/GLM-5.2",
            "subagent_routing": "off",
            "subagent_effective": "inherit",
            "unmatched_native_model": {
              "configured_target": "zai-org/GLM-5.2",
              "effective_route": "anthropic",
              "effective_model": "",
              "effective_source": "global_off"
            },
            "fallback": {
              "active": true,
              "served_route": "anthropic",
              "cause": "http_429"
            },
            "model_catalog": [{
              "label": "GLM-5.2",
              "storage_target": "zai-org/GLM-5.2",
              "alias": "claude-openrouter-glm-5-2",
              "available": true
            }],
            "families": [{
              "family": "opus",
              "configured_target": "zai-org/GLM-5.2",
              "configured_source": "explicit",
              "effective_route": "anthropic",
              "effective_model": "",
              "effective_source": "global_off"
            }]
          }]
        }
        """))

        XCTAssertEqual(snap.token, RoutingToken(
            routerBootID: "boot-a",
            activeGeneration: 42))
        XCTAssertEqual(snap.activeConfigHash, "sha256:active")
        XCTAssertEqual(snap.desiredConfigHash, "sha256:active")
                XCTAssertEqual(snap.capabilities, ["global_routing"])
        XCTAssertFalse(snap.globalRoutingEnabled)
        XCTAssertEqual(snap.reload.state, "applied")

        let client = try! XCTUnwrap(snap.clients.first)
        XCTAssertEqual(client.effectiveSummary, "Native · Anthropic")
        XCTAssertTrue(client.fallbackActive)
        XCTAssertEqual(client.fallback.cause, "http_429")
        XCTAssertEqual(
            client.unmatchedNativeModel?.configuredTarget,
            "zai-org/GLM-5.2")
        XCTAssertEqual(client.subagentEffective, "inherit")
        XCTAssertEqual(client.modelCatalog.first?.target, "zai-org/GLM-5.2")
        XCTAssertEqual(
            client.modelCatalog.first?.alias,
            "claude-openrouter-glm-5-2")

        let family = try! XCTUnwrap(client.families.first)
        XCTAssertEqual(family.configuredTarget, "zai-org/GLM-5.2")
        XCTAssertEqual(family.configuredSource, "explicit")
        XCTAssertEqual(family.effectiveSource, "global_off")

        let routing = RoutingSnapshot(
            status: snap,
            observedAt: Date(timeIntervalSince1970: 10))
        XCTAssertTrue(routing.supportsGlobalRouting)
        XCTAssertTrue(routing.desiredMatchesActive)
    }

    func testAuthStatusParsesCredentialHealthFields() {
        let invalid = AdminStatusSnapshot(dict: decode("""
        {"auth": {"status": "invalid", "source": "keychain",
                  "masked_label": "sk-or-…test",
                  "error": "OpenRouter rejected this key."}}
        """))
        XCTAssertEqual(invalid.auth?.status, "invalid")
        XCTAssertTrue(invalid.auth?.needsAttention == true)
        XCTAssertEqual(
            invalid.auth?.error,
            "OpenRouter rejected this key.")

        let gatewayError = AdminStatusSnapshot(dict: decode("""
        {"auth": {"status": "error", "source": "keychain",
                  "last_error": "OpenRouter key validation request failed",
                  "last_ok_at": "2026-07-29T12:00:00Z"}}
        """))
        XCTAssertEqual(
            gatewayError.auth?.error,
            "OpenRouter key validation request failed")
        XCTAssertEqual(
            gatewayError.auth?.validatedAt,
            "2026-07-29T12:00:00Z")

        let bad = AdminStatusSnapshot(dict: decode(
            #"{"auth": {"status": 3, "limit": false, "error": false}}"#))
        XCTAssertEqual(bad.auth?.status, "unknown")
        XCTAssertNil(bad.auth?.limit)
        XCTAssertEqual(bad.auth?.error, "")
    }
}
