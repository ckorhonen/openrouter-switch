import Foundation
import Security

enum OpenRouterCredentialSource: String, Equatable, Sendable {
    case keychain
    case environment
}

struct OpenRouterCredentialResolution: Equatable, Sendable {
    let source: OpenRouterCredentialSource?
    let isConfigured: Bool

    var isReadOnly: Bool {
        source == .environment
    }
}

enum OpenRouterCredentialStoreError: Error, Equatable, LocalizedError {
    case keychain(OSStatus)

    var errorDescription: String? {
        switch self {
        case .keychain(let status):
            return "Keychain operation failed (status \(status))."
        }
    }
}

protocol OpenRouterCredentialPersisting {
    func readKey() throws -> String?
    func writeKey(_ key: String) throws
    func deleteKey() throws
}

struct SecurityOpenRouterCredentialBackend: OpenRouterCredentialPersisting {
    static let service = "openrouter-switch"
    static let account = "openrouter"
    static let gatewayCredentialReader = "/usr/bin/security"

    func readKey() throws -> String? {
        var query = baseQuery
        query[kSecReturnData as String] = true
        query[kSecMatchLimit as String] = kSecMatchLimitOne
        var result: CFTypeRef?
        let status = SecItemCopyMatching(query as CFDictionary, &result)
        if status == errSecItemNotFound {
            return nil
        }
        guard status == errSecSuccess,
              let data = result as? Data,
              let stored = String(data: data, encoding: .utf8) else {
            throw OpenRouterCredentialStoreError.keychain(status)
        }
        return try Self.decodeStoredKey(stored)
    }

    func writeKey(_ key: String) throws {
        let data = Data(key.utf8)
        let updateStatus = SecItemUpdate(
            baseQuery as CFDictionary,
            Self.updateAttributes(data: data) as CFDictionary)
        if updateStatus == errSecSuccess {
            return
        }
        guard updateStatus == errSecItemNotFound else {
            throw OpenRouterCredentialStoreError.keychain(updateStatus)
        }
        let access = try sharedAccess()
        var add = baseQuery
        add.merge(Self.addAttributes(data: data, access: access)) {
            _, replacement in replacement
        }
        let addStatus = SecItemAdd(add as CFDictionary, nil)
        guard addStatus == errSecSuccess else {
            throw OpenRouterCredentialStoreError.keychain(addStatus)
        }
    }

    func deleteKey() throws {
        let status = SecItemDelete(baseQuery as CFDictionary)
        guard status == errSecSuccess || status == errSecItemNotFound else {
            throw OpenRouterCredentialStoreError.keychain(status)
        }
    }

    static func updateAttributes(data: Data) -> [String: Any] {
        [kSecValueData as String: data]
    }

    static func addAttributes(
        data: Data,
        access: Any
    ) -> [String: Any] {
        [
            kSecValueData as String: data,
            kSecAttrAccess as String: access,
        ]
    }

    private var baseQuery: [String: Any] {
        [
            kSecClass as String: kSecClassGenericPassword,
            kSecAttrService as String: Self.service,
            kSecAttrAccount as String: Self.account,
        ]
    }

    static func decodeStoredKey(_ stored: String) throws -> String {
        let prefix = "go-keyring-base64:"
        guard stored.hasPrefix(prefix) else {
            return stored
        }
        let encoded = String(stored.dropFirst(prefix.count))
        guard let decoded = Data(base64Encoded: encoded),
              let key = String(data: decoded, encoding: .utf8) else {
            throw OpenRouterCredentialStoreError.keychain(errSecDecode)
        }
        return key
    }

    /// The Go gateway uses go-keyring, whose Darwin backend reads through
    /// `/usr/bin/security`. Trust both the current app and that exact system
    /// binary so app-created and CLI-created items remain interoperable
    /// without unattended launchd prompts.
    private func sharedAccess() throws -> SecAccess {
        var currentApplication: SecTrustedApplication?
        var status = SecTrustedApplicationCreateFromPath(
            nil,
            &currentApplication)
        guard status == errSecSuccess,
              let currentApplication else {
            throw OpenRouterCredentialStoreError.keychain(status)
        }

        var gatewayReader: SecTrustedApplication?
        status = Self.gatewayCredentialReader.withCString {
            SecTrustedApplicationCreateFromPath($0, &gatewayReader)
        }
        guard status == errSecSuccess,
              let gatewayReader else {
            throw OpenRouterCredentialStoreError.keychain(status)
        }

        var access: SecAccess?
        status = SecAccessCreate(
            "OpenRouter Switch API key" as CFString,
            [currentApplication, gatewayReader] as CFArray,
            &access)
        guard status == errSecSuccess,
              let access else {
            throw OpenRouterCredentialStoreError.keychain(status)
        }
        return access
    }
}

struct OpenRouterCredentialStore {
    private let backend: any OpenRouterCredentialPersisting
    private let environment: () -> [String: String]

    init(
        backend: any OpenRouterCredentialPersisting =
            SecurityOpenRouterCredentialBackend(),
        environment: @escaping () -> [String: String] = {
            ProcessInfo.processInfo.environment
        }
    ) {
        self.backend = backend
        self.environment = environment
    }

    func resolution() throws -> OpenRouterCredentialResolution {
        do {
            if let key = try backend.readKey(), !normalized(key).isEmpty {
                return OpenRouterCredentialResolution(
                    source: .keychain,
                    isConfigured: true)
            }
        } catch {
            if let fallback = environmentResolution() {
                return fallback
            }
            throw error
        }
        if let fallback = environmentResolution() {
            return fallback
        }
        return OpenRouterCredentialResolution(
            source: nil,
            isConfigured: false)
    }

    func write(_ key: String) throws {
        let candidate = normalized(key)
        guard !candidate.isEmpty else {
            throw OpenRouterKeyValidationError.empty
        }
        try backend.writeKey(candidate)
    }

    func delete() throws {
        try backend.deleteKey()
    }

    private func normalized(_ key: String) -> String {
        key.trimmingCharacters(in: .whitespacesAndNewlines)
    }

    private func environmentResolution()
        -> OpenRouterCredentialResolution? {
        guard let key = environment()["OPENROUTER_API_KEY"],
              !normalized(key).isEmpty else {
            return nil
        }
        return OpenRouterCredentialResolution(
            source: .environment,
            isConfigured: true)
    }
}

struct OpenRouterKeyMetadata: Equatable, Sendable {
    let maskedLabel: String
    let limit: Double?
    let limitRemaining: Double?
    let isFreeTier: Bool?
    let expiresAt: String
}

enum OpenRouterKeyValidationError: Error, Equatable, LocalizedError {
    case empty
    case rejected
    case forbidden
    case malformedResponse
    case requestFailed

    var errorDescription: String? {
        switch self {
        case .empty:
            return "Enter an OpenRouter API key."
        case .rejected:
            return "OpenRouter rejected this API key."
        case .forbidden:
            return "This API key cannot access OpenRouter."
        case .malformedResponse:
            return "OpenRouter returned an invalid key response."
        case .requestFailed:
            return "The OpenRouter API key could not be validated."
        }
    }
}

protocol OpenRouterKeyValidating: Sendable {
    func validate(_ key: String) async throws -> OpenRouterKeyMetadata
}

struct OpenRouterKeyValidator: OpenRouterKeyValidating {
    private let session: URLSession
    private let endpoint: URL

    init(
        session: URLSession? = nil,
        endpoint: URL = URL(string: "https://openrouter.ai/api/v1/key")!
    ) {
        if let session {
            self.session = session
        } else {
            let configuration = URLSessionConfiguration.ephemeral
            configuration.timeoutIntervalForRequest = 10
            configuration.timeoutIntervalForResource = 15
            configuration.urlCache = nil
            self.session = URLSession(configuration: configuration)
        }
        self.endpoint = endpoint
    }

    func validate(_ key: String) async throws -> OpenRouterKeyMetadata {
        let candidate = key.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !candidate.isEmpty else {
            throw OpenRouterKeyValidationError.empty
        }
        var request = URLRequest(url: endpoint)
        request.setValue(
            "Bearer \(candidate)",
            forHTTPHeaderField: "Authorization")
        let data: Data
        let response: URLResponse
        do {
            (data, response) = try await session.data(for: request)
        } catch {
            throw OpenRouterKeyValidationError.requestFailed
        }
        guard let http = response as? HTTPURLResponse else {
            throw OpenRouterKeyValidationError.requestFailed
        }
        switch http.statusCode {
        case 200..<300:
            break
        case 401:
            throw OpenRouterKeyValidationError.rejected
        case 403:
            throw OpenRouterKeyValidationError.forbidden
        default:
            throw OpenRouterKeyValidationError.requestFailed
        }
        guard let object = try? JSONSerialization.jsonObject(with: data),
              let envelope = object as? [String: Any],
              let row = envelope["data"] as? [String: Any] else {
            throw OpenRouterKeyValidationError.malformedResponse
        }
        let expiresAt = try safeExpiration(
            row["expires_at"],
            key: candidate)
        let label: String?
        if let value = row["label"], !(value is NSNull) {
            guard let string = value as? String else {
                throw OpenRouterKeyValidationError.malformedResponse
            }
            label = string
        } else {
            label = nil
        }
        return OpenRouterKeyMetadata(
            maskedLabel: safeKeyLabel(
                label,
                key: candidate),
            limit: try optionalNumber(row["limit"]),
            limitRemaining: try optionalNumber(row["limit_remaining"]),
            isFreeTier: try optionalBool(row["is_free_tier"]),
            expiresAt: expiresAt)
    }

    private func optionalNumber(_ value: Any?) throws -> Double? {
        guard let value, !(value is NSNull), !(value is Bool) else {
            if value is Bool {
                throw OpenRouterKeyValidationError.malformedResponse
            }
            return nil
        }
        if let value = value as? NSNumber {
            return value.doubleValue
        }
        throw OpenRouterKeyValidationError.malformedResponse
    }

    private func optionalBool(_ value: Any?) throws -> Bool? {
        guard let value, !(value is NSNull) else { return nil }
        guard let value = value as? Bool else {
            throw OpenRouterKeyValidationError.malformedResponse
        }
        return value
    }

    private func safeKeyLabel(_ value: String?, key: String) -> String {
        let label = value?
            .trimmingCharacters(in: .whitespacesAndNewlines) ?? ""
        guard !label.isEmpty,
              label.count <= 128,
              !label.contains(key) else {
            return "configured key"
        }
        return label
    }

    private func safeExpiration(_ value: Any?, key: String) throws -> String {
        guard let value, !(value is NSNull) else { return "" }
        guard let raw = value as? String,
              !raw.contains(key) else {
            throw OpenRouterKeyValidationError.malformedResponse
        }
        let formatter = ISO8601DateFormatter()
        formatter.formatOptions = [
            .withInternetDateTime,
            .withFractionalSeconds,
        ]
        if let date = formatter.date(from: raw) {
            return formatter.string(from: date)
        }
        formatter.formatOptions = [.withInternetDateTime]
        guard let date = formatter.date(from: raw) else {
            throw OpenRouterKeyValidationError.malformedResponse
        }
        return formatter.string(from: date)
    }
}
