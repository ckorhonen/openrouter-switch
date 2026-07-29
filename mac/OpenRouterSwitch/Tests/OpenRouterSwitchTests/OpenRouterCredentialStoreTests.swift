import XCTest
@testable import OpenRouterSwitch

private final class CredentialValidationURLProtocol: URLProtocol {
    static var responseData = Data()
    static var statusCode = 200
    static var authorization = ""

    override class func canInit(with request: URLRequest) -> Bool {
        true
    }

    override class func canonicalRequest(for request: URLRequest)
        -> URLRequest {
        request
    }

    override func startLoading() {
        Self.authorization =
            request.value(forHTTPHeaderField: "Authorization") ?? ""
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

private final class MemoryCredentialBackend: OpenRouterCredentialPersisting {
    var key: String?
    var readError: Error?
    var writeError: Error?
    var deleteError: Error?

    func readKey() throws -> String? {
        if let readError { throw readError }
        return key
    }

    func writeKey(_ key: String) throws {
        if let writeError { throw writeError }
        self.key = key
    }

    func deleteKey() throws {
        if let deleteError { throw deleteError }
        key = nil
    }
}

final class OpenRouterCredentialStoreTests: XCTestCase {
    func testReadWriteDeleteUsesInjectedBackend() throws {
        let backend = MemoryCredentialBackend()
        let store = OpenRouterCredentialStore(
            backend: backend,
            environment: { [:] })

        XCTAssertEqual(
            try store.resolution(),
            OpenRouterCredentialResolution(source: nil, isConfigured: false))

        try store.write("  sk-or-test  \n")
        XCTAssertEqual(backend.key, "sk-or-test")
        XCTAssertEqual(
            try store.resolution(),
            OpenRouterCredentialResolution(
                source: .keychain,
                isConfigured: true))

        try store.delete()
        XCTAssertNil(backend.key)
    }

    func testKeychainPrecedesEnvironment() throws {
        let backend = MemoryCredentialBackend()
        backend.key = "keychain-secret"
        let store = OpenRouterCredentialStore(
            backend: backend,
            environment: { ["OPENROUTER_API_KEY": "environment-secret"] })

        XCTAssertEqual(
            try store.resolution(),
            OpenRouterCredentialResolution(
                source: .keychain,
                isConfigured: true))
    }

    func testEnvironmentFallbackIsReadOnly() throws {
        let store = OpenRouterCredentialStore(
            backend: MemoryCredentialBackend(),
            environment: { ["OPENROUTER_API_KEY": "environment-secret"] })

        let resolution = try store.resolution()
        XCTAssertEqual(resolution.source, .environment)
        XCTAssertTrue(resolution.isReadOnly)
    }

    func testEnvironmentFallbackSurvivesKeychainReadFailure() throws {
        let backend = MemoryCredentialBackend()
        backend.readError = OpenRouterCredentialStoreError.keychain(-25308)
        let store = OpenRouterCredentialStore(
            backend: backend,
            environment: {
                ["OPENROUTER_API_KEY": "environment-secret"]
            })

        let resolution = try store.resolution()

        XCTAssertEqual(resolution.source, .environment)
        XCTAssertTrue(resolution.isReadOnly)
    }

    func testKeychainReadFailureWithoutEnvironmentStillFails() {
        let backend = MemoryCredentialBackend()
        backend.readError = OpenRouterCredentialStoreError.keychain(-25308)
        let store = OpenRouterCredentialStore(
            backend: backend,
            environment: { [:] })

        XCTAssertThrowsError(try store.resolution())
    }

    func testEmptyEnvironmentIsNotConfigured() throws {
        let store = OpenRouterCredentialStore(
            backend: MemoryCredentialBackend(),
            environment: { ["OPENROUTER_API_KEY": "  \n"] })

        XCTAssertFalse(try store.resolution().isConfigured)
    }

    func testErrorsNeverContainCredentialText() {
        let backend = MemoryCredentialBackend()
        backend.writeError = OpenRouterCredentialStoreError.keychain(-50)
        let store = OpenRouterCredentialStore(
            backend: backend,
            environment: { [:] })
        let secret = "sk-or-never-print-this"

        XCTAssertThrowsError(try store.write(secret)) { error in
            XCTAssertFalse(String(describing: error).contains(secret))
            XCTAssertFalse(
                (error as? LocalizedError)?.errorDescription?
                    .contains(secret) ?? false)
        }
    }

    func testSecurityBackendUsesSharedIdentity() {
        XCTAssertEqual(
            SecurityOpenRouterCredentialBackend.service,
            "openrouter-switch")
        XCTAssertEqual(
            SecurityOpenRouterCredentialBackend.account,
            "openrouter")
        XCTAssertEqual(
            SecurityOpenRouterCredentialBackend.gatewayCredentialReader,
            "/usr/bin/security")
    }

    func testSecurityBackendUpdateAttributesOmitAccessControl() {
        let data = Data("replacement-key".utf8)

        let attributes =
            SecurityOpenRouterCredentialBackend.updateAttributes(data: data)

        XCTAssertEqual(attributes.count, 1)
        XCTAssertEqual(attributes[kSecValueData as String] as? Data, data)
        XCTAssertNil(attributes[kSecAttrAccess as String])
    }

    func testSecurityBackendAddAttributesRetainAccessControl() {
        let data = Data("new-key".utf8)
        let access = NSObject()

        let attributes = SecurityOpenRouterCredentialBackend.addAttributes(
            data: data,
            access: access)

        XCTAssertEqual(attributes.count, 2)
        XCTAssertEqual(attributes[kSecValueData as String] as? Data, data)
        XCTAssertTrue(
            attributes[kSecAttrAccess as String] as AnyObject === access)
    }

    func testSecurityBackendDecodesGoKeyringStorageFormat() throws {
        XCTAssertEqual(
            try SecurityOpenRouterCredentialBackend.decodeStoredKey(
                "go-keyring-base64:c2stb3ItdGVzdA=="),
            "sk-or-test")
        XCTAssertEqual(
            try SecurityOpenRouterCredentialBackend.decodeStoredKey(
                "raw-swift-key"),
            "raw-swift-key")
        XCTAssertThrowsError(
            try SecurityOpenRouterCredentialBackend.decodeStoredKey(
                "go-keyring-base64:not-valid-base64!"))
    }

    func testValidatorNeverPublishesCredentialAsMetadata() async throws {
        let secret = "sk-or-never-publish-this"
        CredentialValidationURLProtocol.responseData = Data("""
        {"data":{"label":"\(secret)","limit":20,"limit_remaining":12}}
        """.utf8)
        CredentialValidationURLProtocol.statusCode = 200
        CredentialValidationURLProtocol.authorization = ""
        let configuration = URLSessionConfiguration.ephemeral
        configuration.protocolClasses = [CredentialValidationURLProtocol.self]
        let validator = OpenRouterKeyValidator(
            session: URLSession(configuration: configuration))

        let metadata = try await validator.validate(secret)

        XCTAssertTrue(
            CredentialValidationURLProtocol.authorization
                == "Bearer \(secret)")
        XCTAssertEqual(metadata.maskedLabel, "configured key")
        XCTAssertFalse(String(describing: metadata).contains(secret))
    }

    func testValidatorRejectsMalformedSuccessEnvelope() async {
        CredentialValidationURLProtocol.responseData = Data("{}".utf8)
        CredentialValidationURLProtocol.statusCode = 200
        let configuration = URLSessionConfiguration.ephemeral
        configuration.protocolClasses = [CredentialValidationURLProtocol.self]
        let validator = OpenRouterKeyValidator(
            session: URLSession(configuration: configuration))

        do {
            _ = try await validator.validate("sk-or-test")
            XCTFail("expected malformed response rejection")
        } catch {
            XCTAssertEqual(
                error as? OpenRouterKeyValidationError,
                .malformedResponse)
        }
    }

    func testValidatorRejectsCredentialReflectedInExpiration() async {
        let secret = "sk-or-never-retain-this"
        CredentialValidationURLProtocol.responseData = Data("""
        {"data":{"label":"safe label","expires_at":"\(secret)"}}
        """.utf8)
        CredentialValidationURLProtocol.statusCode = 200
        let configuration = URLSessionConfiguration.ephemeral
        configuration.protocolClasses = [CredentialValidationURLProtocol.self]
        let validator = OpenRouterKeyValidator(
            session: URLSession(configuration: configuration))

        do {
            _ = try await validator.validate(secret)
            XCTFail("expected unsafe expiration rejection")
        } catch {
            XCTAssertEqual(
                error as? OpenRouterKeyValidationError,
                .malformedResponse)
            XCTAssertFalse(String(describing: error).contains(secret))
        }
    }
}
