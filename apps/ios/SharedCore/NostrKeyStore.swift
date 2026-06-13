import Foundation
import Security

enum NostrKeyImportError: Error, LocalizedError, Equatable {
    case empty
    case publicKey
    case invalid

    var errorDescription: String? {
        switch self {
        case .empty: return "Enter an nsec or 64-character private key hex."
        case .publicKey: return "That is a public key. Import your nsec or private key hex instead."
        case .invalid: return "Invalid key. Paste an nsec or 64-character private key hex."
        }
    }
}

protocol NostrKeyStoring {
    func importSecret(_ raw: String) throws
    func generateSecret() throws
    func clearSecret() throws
    func currentSecretHex() -> String?
    func currentPublicKeyHex() -> String?
    func currentNpub() -> String?
    func sign(_ unsigned: UnsignedNostrEvent) throws -> NostrEvent
}

final class NostrKeyStore: NostrKeyStoring {
    private let service: String
    private let account: String
    private let cryptoProvider: NostrCryptoProviding
    private var secretHex: String?

    init(
        service: String = "org.trustroots.wrapster.ios",
        account: String = "wrapster.privatekey.hex",
        cryptoProvider: NostrCryptoProviding = NostrSDKCryptoProvider()
    ) {
        self.service = service
        self.account = account
        self.cryptoProvider = cryptoProvider
        self.secretHex = Self.loadSecret(service: service, account: account)
    }

    func importSecret(_ raw: String) throws {
        let secret = try Self.parseSecret(raw)
        _ = try cryptoProvider.publicKeyHex(fromSecretHex: secret)
        try Self.saveSecret(secret, service: service, account: account)
        secretHex = secret
    }

    func generateSecret() throws {
        var bytes = Data(count: 32)
        let status = bytes.withUnsafeMutableBytes { SecRandomCopyBytes(kSecRandomDefault, 32, $0.baseAddress!) }
        guard status == errSecSuccess else { throw KeyStoreError.invalidInput }
        try importSecret(Hex.encode(bytes))
    }

    func clearSecret() throws {
        let query: [String: Any] = [
            kSecClass as String: kSecClassGenericPassword,
            kSecAttrService as String: service,
            kSecAttrAccount as String: account
        ]
        SecItemDelete(query as CFDictionary)
        #if targetEnvironment(simulator)
        UserDefaults.standard.removeObject(forKey: Self.simulatorFallbackKey(service: service, account: account))
        #endif
        secretHex = nil
    }

    func currentSecretHex() -> String? { secretHex }

    func currentPublicKeyHex() -> String? {
        guard let secretHex else { return nil }
        return try? cryptoProvider.publicKeyHex(fromSecretHex: secretHex)
    }

    func currentNpub() -> String? {
        guard let pubkey = currentPublicKeyHex() else { return nil }
        return try? NIP19.encodeNpub(pubkeyHex: pubkey)
    }

    func sign(_ unsigned: UnsignedNostrEvent) throws -> NostrEvent {
        guard let secretHex else { throw KeyStoreError.missingSecret }
        let pubkey = try cryptoProvider.publicKeyHex(fromSecretHex: secretHex)
        let normalized = UnsignedNostrEvent(
            pubkey: pubkey,
            createdAt: unsigned.createdAt,
            kind: unsigned.kind,
            tags: unsigned.tags,
            content: unsigned.content
        )
        let eventID = try NIP01.eventIDHex(for: normalized)
        let signature = try cryptoProvider.signEventIDHex(eventID, secretHex: secretHex)
        return NostrEvent(
            id: eventID,
            pubkey: pubkey,
            createdAt: normalized.createdAt,
            kind: normalized.kind,
            tags: normalized.tags,
            content: normalized.content,
            sig: signature
        )
    }

    static func parseSecret(_ raw: String) throws -> String {
        let value = raw.trimmingCharacters(in: .whitespacesAndNewlines).lowercased()
        guard !value.isEmpty else { throw NostrKeyImportError.empty }
        if value.hasPrefix("npub1") { throw NostrKeyImportError.publicKey }
        if value.hasPrefix("nsec1") { return try NIP19.importSecret(value) }
        if value.count == 64, NIP19.isValidHex(value, expectedBytes: 32) { return value }
        throw NostrKeyImportError.invalid
    }

    private static func saveSecret(_ secret: String, service: String, account: String) throws {
        let data = Data(secret.utf8)
        let query: [String: Any] = [
            kSecClass as String: kSecClassGenericPassword,
            kSecAttrService as String: service,
            kSecAttrAccount as String: account
        ]
        let update: [String: Any] = [kSecValueData as String: data]
        let updateStatus = SecItemUpdate(query as CFDictionary, update as CFDictionary)
        if updateStatus == errSecSuccess { return }
        if updateStatus != errSecItemNotFound {
            #if targetEnvironment(simulator)
            UserDefaults.standard.set(secret, forKey: simulatorFallbackKey(service: service, account: account))
            return
            #else
            throw KeyStoreError.invalidInput
            #endif
        }
        var insert = query
        insert[kSecValueData as String] = data
        insert[kSecAttrAccessible as String] = kSecAttrAccessibleAfterFirstUnlockThisDeviceOnly
        let addStatus = SecItemAdd(insert as CFDictionary, nil)
        if addStatus == errSecSuccess { return }
        #if targetEnvironment(simulator)
        UserDefaults.standard.set(secret, forKey: simulatorFallbackKey(service: service, account: account))
        #else
        throw KeyStoreError.invalidInput
        #endif
    }

    private static func loadSecret(service: String, account: String) -> String? {
        let query: [String: Any] = [
            kSecClass as String: kSecClassGenericPassword,
            kSecAttrService as String: service,
            kSecAttrAccount as String: account,
            kSecReturnData as String: true,
            kSecMatchLimit as String: kSecMatchLimitOne
        ]
        var item: CFTypeRef?
        let status = SecItemCopyMatching(query as CFDictionary, &item)
        if status == errSecSuccess, let data = item as? Data, let value = String(data: data, encoding: .utf8) {
            return value
        }
        #if targetEnvironment(simulator)
        return UserDefaults.standard.string(forKey: simulatorFallbackKey(service: service, account: account))
        #else
        return nil
        #endif
    }

    private static func simulatorFallbackKey(service: String, account: String) -> String {
        "wrapster.simulator-keychain-fallback.\(service).\(account)"
    }
}
