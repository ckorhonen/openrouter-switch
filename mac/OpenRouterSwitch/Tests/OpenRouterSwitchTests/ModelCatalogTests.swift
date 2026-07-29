import Foundation
import ServiceManagement
import XCTest
@testable import OpenRouterSwitch

private actor FixedModelCatalogReader: ModelCatalogReading {
    let result: Result<LiveModelCatalogSnapshot, Error>
    private(set) var calls = 0

    init(_ result: Result<LiveModelCatalogSnapshot, Error>) {
        self.result = result
    }

    func fetchModelCatalog() async throws -> LiveModelCatalogSnapshot {
        calls += 1
        return try result.get()
    }
}

private actor SequencedModelCatalogReader: ModelCatalogReading {
    private let first: LiveModelCatalogSnapshot
    private let second: LiveModelCatalogSnapshot
    private var calls = 0

    init(first: LiveModelCatalogSnapshot,
         second: LiveModelCatalogSnapshot) {
        self.first = first
        self.second = second
    }

    func fetchModelCatalog() async throws -> LiveModelCatalogSnapshot {
        calls += 1
        if calls == 1 {
            // Deliberately ignore cancellation. The generation guard must
            // still prevent this result from replacing the newer response.
            try? await Task.sleep(nanoseconds: 120_000_000)
            return first
        }
        return second
    }
}

private actor DelayedModelCatalogReader: ModelCatalogReading {
    let snapshot: LiveModelCatalogSnapshot

    init(snapshot: LiveModelCatalogSnapshot) {
        self.snapshot = snapshot
    }

    func fetchModelCatalog() async throws -> LiveModelCatalogSnapshot {
        try? await Task.sleep(nanoseconds: 80_000_000)
        return snapshot
    }
}

private actor FixedModelCatalogAdminReader: AdminStatusReading {
    let snapshot: AdminStatusSnapshot

    init(snapshot: AdminStatusSnapshot) {
        self.snapshot = snapshot
    }

    func fetchStatus() async throws -> AdminStatusSnapshot {
        snapshot
    }

    func fetchStats(windowSeconds: Int,
                    bucketSeconds: Int) async throws -> StatsSnapshot {
        StatsSnapshot(dict: [:])
    }
}

@MainActor
private final class ModelCatalogLoginItemService: LoginItemServicing {
    var status: SMAppService.Status = .notRegistered
    func reconcileAtLaunch() {}
    func toggle() {}
    func openSystemSettings() {}
}

private final class ModelCatalogURLProtocol: URLProtocol {
    static var responseData = Data()
    static var statusCode = 200
    static var observedURL: URL?
    static var observedTimeout: TimeInterval?

    override class func canInit(with request: URLRequest) -> Bool {
        true
    }

    override class func canonicalRequest(for request: URLRequest)
        -> URLRequest {
        request
    }

    override func startLoading() {
        Self.observedURL = request.url
        Self.observedTimeout = request.timeoutInterval
        let response = HTTPURLResponse(
            url: request.url!,
            statusCode: Self.statusCode,
            httpVersion: nil,
            headerFields: ["Content-Type": "application/json"])!
        client?.urlProtocol(
            self,
            didReceive: response,
            cacheStoragePolicy: .notAllowed)
        client?.urlProtocol(self, didLoad: Self.responseData)
        client?.urlProtocolDidFinishLoading(self)
    }

    override func stopLoading() {}
}

final class ModelCatalogTests: XCTestCase {
    func testGatewayClientUsesEndpointSpecificRequestTimeouts() async throws {
        let configuration = URLSessionConfiguration.ephemeral
        configuration.timeoutIntervalForRequest = 60
        configuration.timeoutIntervalForResource = 60
        configuration.protocolClasses = [ModelCatalogURLProtocol.self]
        let client = GatewayAPIClient(
            runtime: .stable(),
            session: URLSession(configuration: configuration))

        ModelCatalogURLProtocol.responseData = Data("{}".utf8)
        ModelCatalogURLProtocol.statusCode = 200
        ModelCatalogURLProtocol.observedTimeout = nil
        _ = try await client.fetchStatus()
        XCTAssertEqual(
            try XCTUnwrap(ModelCatalogURLProtocol.observedTimeout),
            2,
            accuracy: 0.001)

        ModelCatalogURLProtocol.responseData = Data("""
        {"auth":{"state":"valid","source":"keychain"}}
        """.utf8)
        ModelCatalogURLProtocol.observedTimeout = nil
        _ = try await client.reloadCredentials()
        XCTAssertEqual(
            try XCTUnwrap(ModelCatalogURLProtocol.observedTimeout),
            7,
            accuracy: 0.001)

        ModelCatalogURLProtocol.responseData = Data("""
        {"state":"ready","models":[]}
        """.utf8)
        ModelCatalogURLProtocol.observedTimeout = nil
        _ = try await client.fetchModelCatalog()
        XCTAssertEqual(
            try XCTUnwrap(ModelCatalogURLProtocol.observedTimeout),
            22,
            accuracy: 0.001)
    }

    func testGatewayClientDecodesNarrowModelCatalogContract() async throws {
        ModelCatalogURLProtocol.responseData = Data("""
        {
          "state": "ready",
          "models": [
            {
              "slug": "zai-org/GLM-5.2",
              "display_name": "GLM 5.2",
              "tool_capable": true,
              "context_tokens": 131072,
              "max_output_tokens": 32768,
              "input_modalities": ["text"],
              "output_modalities": ["text"],
              "supported_parameters": ["tools", "reasoning"]
            }
          ],
          "fetched_at": "2026-07-24T18:00:00Z"
        }
        """.utf8)
        ModelCatalogURLProtocol.statusCode = 200
        ModelCatalogURLProtocol.observedURL = nil
        let configuration = URLSessionConfiguration.ephemeral
        configuration.protocolClasses = [ModelCatalogURLProtocol.self]
        let client = GatewayAPIClient(
            runtime: .stable(),
            session: URLSession(configuration: configuration))

        let snapshot = try await client.fetchModelCatalog()

        XCTAssertEqual(snapshot.state, .ready)
        XCTAssertNil(snapshot.unavailableReason)
        XCTAssertEqual(snapshot.models.count, 1)
        XCTAssertEqual(snapshot.models[0].slug, "zai-org/GLM-5.2")
        XCTAssertEqual(snapshot.models[0].displayName, "GLM 5.2")
        XCTAssertTrue(snapshot.models[0].toolCapable)
        XCTAssertEqual(snapshot.models[0].contextTokens, 131_072)
        XCTAssertEqual(snapshot.models[0].maxOutputTokens, 32_768)
        XCTAssertEqual(snapshot.models[0].supportedParameters, [
            "tools", "reasoning",
        ])
        XCTAssertEqual(
            ModelCatalogURLProtocol.observedURL?.path,
            "/v1/admin/model-catalog")
    }

    func testModelCatalogEntryRejectsMissingOrInvalidRequiredFields() {
        let valid: [String: Any] = [
            "slug": "vendor/model",
            "display_name": "Model",
            "tool_capable": true,
            "input_modalities": ["text"],
            "output_modalities": ["text"],
            "supported_parameters": ["tools"],
        ]
        XCTAssertNotNil(LiveModelCatalogEntry(dict: valid))
        let omittedArrays = LiveModelCatalogEntry(dict: [
            "slug": "vendor/metadata-light",
            "display_name": "Metadata Light",
            "tool_capable": true,
        ])
        XCTAssertNotNil(omittedArrays)
        XCTAssertFalse(omittedArrays?.selectable ?? true)

        for invalid in [
            [
                "display_name": "Model",
            ] as [String: Any],
            [
                "slug": "   ",
                "display_name": "Model",
            ],
            [
                "slug": "vendor/model",
            ],
            [
                "slug": "vendor/model",
                "display_name": 42,
            ],
        ] {
            XCTAssertNil(LiveModelCatalogEntry(dict: invalid))
        }
    }

    func testLiveModelCatalogUsesRawSlugAsFinalDisplayFallback() {
        let model = LiveModelCatalogEntry(dict: [
            "slug": "vendor/model-with-hyphens",
            "display_name": "",
            "tool_capable": true,
            "input_modalities": ["text"],
            "output_modalities": ["text"],
            "supported_parameters": ["tools"],
        ])

        XCTAssertEqual(model?.displayLabel, "vendor/model-with-hyphens")
    }

    func testModelCatalogSnapshotRejectsMalformedEnvelopeOrAnyRow() {
        let validModel: [String: Any] = [
            "slug": "vendor/model",
            "display_name": "Model",
            "tool_capable": true,
            "input_modalities": ["text"],
            "output_modalities": ["text"],
            "supported_parameters": ["tools"],
        ]
        let valid: [String: Any] = [
            "state": "ready",
            "models": [validModel],
            "fetched_at": "2026-07-24T18:00:00Z",
        ]
        XCTAssertNotNil(LiveModelCatalogSnapshot(dict: valid))

        var invalidEnvelopes: [[String: Any]] = [
            [
                "models": [validModel],
                "fetched_at": "",
                "error": "",
            ],
            [
                "state": "unknown",
                "unavailable_reason": "",
                "models": [validModel],
                "fetched_at": "",
                "error": "",
            ],
            [
                "state": "ready",
                "unavailable_reason": "",
                "models": "not-an-array",
                "fetched_at": "",
                "error": "",
            ],
            [
                "state": "ready",
                "unavailable_reason": "invalid_credentials",
                "models": [validModel],
                "fetched_at": "",
                "error": "",
            ],
            [
                "state": "unavailable",
                "unavailable_reason": "unknown",
                "models": [],
                "fetched_at": "",
                "error": "",
            ],
        ]
        var malformedRow = valid
        malformedRow["models"] = [
            validModel,
            [
                "slug": "vendor/bad",
                "display_name": 1,
            ],
        ]
        invalidEnvelopes.append(malformedRow)

        for invalid in invalidEnvelopes {
            XCTAssertNil(LiveModelCatalogSnapshot(dict: invalid))
        }
    }

    func testGatewayClientRejectsMalformedCatalogRow() async {
        ModelCatalogURLProtocol.responseData = Data("""
        {
          "state": "ready",
          "models": [
            {
              "slug": "vendor/model",
              "tool_capable": true,
              "input_modalities": ["text"],
              "output_modalities": ["text"],
              "supported_parameters": ["tools"]
            }
          ],
          "unavailable_reason": "",
          "fetched_at": "2026-07-24T18:00:00Z",
          "error": ""
        }
        """.utf8)
        ModelCatalogURLProtocol.statusCode = 200
        let configuration = URLSessionConfiguration.ephemeral
        configuration.protocolClasses = [ModelCatalogURLProtocol.self]
        let client = GatewayAPIClient(
            runtime: .stable(),
            session: URLSession(configuration: configuration))

        do {
            _ = try await client.fetchModelCatalog()
            XCTFail("Expected the malformed row to reject the payload")
        } catch {
            XCTAssertEqual(
                error as? GatewayClientError,
                GatewayClientError.invalidPayload)
        }
    }

    func testProjectionUsesOneLiveBackedListAndPreservesAliasMatching() {
        let configured = [
            configuredModel(
                label: "Configured A",
                target: "claude-openrouter-a",
                slug: "vendor/a",
                available: false),
            configuredModel(
                label: "Configured B",
                target: "claude-openrouter-b",
                slug: "vendor/b",
                available: true),
            configuredModel(
                label: "Private C",
                target: "private-c",
                slug: "private/c",
                available: false),
        ]
        let live = [
            liveModel("vendor/a", label: "Live A"),
            liveModel("vendor/b", label: "Live B"),
            liveModel("vendor/d", label: "Live D"),
            liveModel("vendor/e", label: "Live E"),
            liveModel("vendor/d", label: "Duplicate D"),
            LiveModelCatalogEntry(dict: [
                "slug": "vendor/no-tools",
                "display_name": "No Tools",
                "tool_capable": false,
                "input_modalities": ["text"],
                "output_modalities": ["text"],
                "supported_parameters": [],
            ])!,
        ]

        let projection = projectModelCatalog(
            configured: configured,
            liveState: .ready(live))

        XCTAssertEqual(
            projection.selectable.map(\.target),
            [
                "vendor/a",
                "vendor/b",
                "vendor/d",
                "vendor/e",
            ])
        XCTAssertEqual(projection.selectable[0].label, "Live A")
        XCTAssertEqual(projection.selectable[0].slug, "vendor/a")
        XCTAssertEqual(projection.selectable[0].alias, "claude-openrouter-a")
        XCTAssertFalse(
            projection.selectable.contains(where: {
                $0.slug == "private/c" || $0.slug == "vendor/no-tools"
            }))
    }

    func testNonReadyStateDoesNotPresentConfiguredModelsAsAvailable() {
        let configured = [
            configuredModel(
                label: "Configured",
                target: "configured",
                slug: "vendor/configured",
                available: true),
        ]

        for state in [
            LiveModelCatalogLoadState.idle,
            .loading,
            .unavailable(.missingCredentials),
            .unavailable(.invalidCredentials),
            .error("unavailable"),
        ] {
            let projection = projectModelCatalog(
                configured: configured,
                liveState: state)
            XCTAssertTrue(projection.selectable.isEmpty)
        }
    }

    func testLiveModelAPIDispatchesItsRawSlug() {
        let projection = projectModelCatalog(
            configured: [],
            liveState: .ready([
                liveModel("zai-org/GLM-5.2", label: "GLM 5.2"),
            ]))
        let model = projection.selectable[0]

        XCTAssertEqual(model.target, "zai-org/GLM-5.2")
        XCTAssertEqual(
            familyDispatchArgs(
                client: "claude-code",
                family: "opus",
                choice: .catalog(model)),
            ["claude", "route", "opus", "zai-org/GLM-5.2"])
        XCTAssertEqual(
            subagentDispatchArgs(
                client: "claude-code",
                choice: .catalog(model)),
            ["claude", "subagents", "zai-org/GLM-5.2"])
    }

    @MainActor
    func testCatalogRefreshAppliesReadySignedOutAndErrorStates() async {
        let ready = catalogSnapshot(
            state: .ready,
            models: [liveModel("vendor/ready")])
        let reader = FixedModelCatalogReader(.success(ready))
        let state = makeState(modelCatalogReader: reader)

        state.ensureModelCatalogLoaded()
        XCTAssertEqual(state.liveModelCatalogState, .loading)
        state.ensureModelCatalogLoaded()
        await state.waitForModelCatalogRefresh()
        XCTAssertEqual(state.liveModelCatalogState, .ready(ready.models))
        state.ensureModelCatalogLoaded()
        await state.waitForModelCatalogRefresh()
        let calls = await reader.calls
        XCTAssertEqual(calls, 1)

        let notSignedInState = makeState(modelCatalogReader:
            FixedModelCatalogReader(.success(catalogSnapshot(
                state: .unavailable,
                unavailableReason: .missingCredentials))))
        notSignedInState.requestModelCatalogRefresh()
        await notSignedInState.waitForModelCatalogRefresh()
        XCTAssertEqual(
            notSignedInState.liveModelCatalogState,
            .unavailable(.missingCredentials))

        let expiredState = makeState(modelCatalogReader:
            FixedModelCatalogReader(.success(catalogSnapshot(
                state: .unavailable,
                unavailableReason: .invalidCredentials))))
        expiredState.requestModelCatalogRefresh()
        await expiredState.waitForModelCatalogRefresh()
        XCTAssertEqual(
            expiredState.liveModelCatalogState,
            .unavailable(.invalidCredentials))
        XCTAssertEqual(
            liveModelCatalogUnavailableMessage(.missingCredentials),
            "Add an OpenRouter API key to load account models.")
        XCTAssertEqual(
            liveModelCatalogUnavailableMessage(.invalidCredentials),
            "The OpenRouter API key is invalid. Replace it to load account models.")

        let backendErrorState = makeState(modelCatalogReader:
            FixedModelCatalogReader(.success(catalogSnapshot(
                state: .error,
                error: "Catalog is temporarily unavailable."))))
        backendErrorState.requestModelCatalogRefresh()
        await backendErrorState.waitForModelCatalogRefresh()
        XCTAssertEqual(
            backendErrorState.liveModelCatalogState,
            .error("Catalog is temporarily unavailable."))
    }

    @MainActor
    func testNewerCatalogRefreshWinsWhenCancelledReaderReturnsLate() async {
        let old = catalogSnapshot(
            state: .ready,
            models: [liveModel("vendor/old")])
        let newest = catalogSnapshot(
            state: .ready,
            models: [liveModel("vendor/new")])
        let state = makeState(modelCatalogReader:
            SequencedModelCatalogReader(first: old, second: newest))

        state.requestModelCatalogRefresh()
        try? await Task.sleep(nanoseconds: 10_000_000)
        state.requestModelCatalogRefresh()
        await state.waitForModelCatalogRefresh()
        try? await Task.sleep(nanoseconds: 140_000_000)

        XCTAssertEqual(state.liveModelCatalogState, .ready(newest.models))
    }

    @MainActor
    func testWindowCloseInvalidatesLateCatalogResult() async {
        let ready = catalogSnapshot(
            state: .ready,
            models: [liveModel("vendor/late")])
        let state = makeState(modelCatalogReader:
            DelayedModelCatalogReader(snapshot: ready))

        state.ensureModelCatalogLoaded()
        state.routerWindowDidClose()
        try? await Task.sleep(nanoseconds: 100_000_000)

        XCTAssertEqual(state.liveModelCatalogState, .idle)
    }

    @MainActor
    func testCatalogFailureDoesNotStaleConfirmedRoutingSnapshot() async {
        let catalogReader = FixedModelCatalogReader(
            .failure(GatewayClientError.badResponse(503)))
        let state = makeState(
            adminReader: FixedModelCatalogAdminReader(
                snapshot: adminSnapshot()),
            modelCatalogReader: catalogReader)
        await state.refresh()
        XCTAssertNotNil(state.routingSnapshot)
        XCTAssertFalse(state.snapshotIsStale)

        state.requestModelCatalogRefresh()
        await state.waitForModelCatalogRefresh()

        XCTAssertNotNil(state.routingSnapshot)
        XCTAssertFalse(state.snapshotIsStale)
        XCTAssertEqual(
            state.liveModelCatalogState,
            .error("Live model availability could not be loaded."))
        XCTAssertNil(state.lastError)
    }

    private func configuredModel(
        label: String,
        target: String,
        slug: String,
        available: Bool
    ) -> ModelCatalogEntry {
        ModelCatalogEntry(dict: [
            "label": label,
            "storage_target": target,
            "slug": slug,
            "alias": target,
            "available": available,
        ])!
    }

    private func liveModel(
        _ slug: String,
        label: String = ""
    ) -> LiveModelCatalogEntry {
        LiveModelCatalogEntry(dict: [
            "slug": slug,
            "display_name": label,
            "tool_capable": true,
            "context_tokens": 131_072,
            "max_output_tokens": 32_768,
            "input_modalities": ["text"],
            "output_modalities": ["text"],
            "supported_parameters": ["tools"],
        ])!
    }

    private func catalogSnapshot(
        state: LiveModelCatalogResponseState,
        models: [LiveModelCatalogEntry] = [],
        unavailableReason: LiveModelCatalogUnavailableReason? = nil,
        error: String = ""
    ) -> LiveModelCatalogSnapshot {
        LiveModelCatalogSnapshot(dict: [
            "state": state.rawValue,
            "unavailable_reason": unavailableReason?.rawValue ?? "",
            "models": models.map {
                [
                    "slug": $0.slug,
                    "display_name": $0.displayName,
                    "tool_capable": $0.toolCapable,
                    "context_tokens": $0.contextTokens as Any,
                    "max_output_tokens": $0.maxOutputTokens as Any,
                    "input_modalities": $0.inputModalities,
                    "output_modalities": $0.outputModalities,
                    "supported_parameters": $0.supportedParameters,
                ] as [String: Any]
            },
            "fetched_at": "2026-07-24T18:00:00Z",
            "error": error,
        ])!
    }

    private func adminSnapshot() -> AdminStatusSnapshot {
        AdminStatusSnapshot(dict: [
            "router_boot_id": "boot-a",
            "active_generation": 1,
            "active_config_hash": "sha256:active",
            "desired_config_hash": "sha256:active",
            "capabilities": ["global_routing"],
            "global_routing_enabled": true,
            "clients": [],
        ])
    }

    @MainActor
    private func makeState(
        adminReader: (any AdminStatusReading)? = nil,
        modelCatalogReader: any ModelCatalogReading
    ) -> OpenRouterSwitchState {
        OpenRouterSwitchState(
            variant: .resolve(
                infoDictionary: [:],
                environment: [:]),
            reader: adminReader ?? FixedModelCatalogAdminReader(
                snapshot: adminSnapshot()),
            modelCatalogReader: modelCatalogReader,
            loginItemService: ModelCatalogLoginItemService(),
            startPolling: false)
    }
}
