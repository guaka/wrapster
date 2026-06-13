import Foundation

enum NIP98SignerError: Error, LocalizedError {
    case invalidURL
    case encodingFailed

    var errorDescription: String? {
        switch self {
        case .invalidURL: return "The request URL is invalid."
        case .encodingFailed: return "Unable to encode NIP-98 authorization."
        }
    }
}

struct NIP98Signer {
    static let eventKind = 27235
    let keyStore: NostrKeyStoring
    var now: () -> Date = Date.init

    func authorizationHeader(url: URL, method: String) throws -> String {
        guard let pubkey = keyStore.currentPublicKeyHex() else { throw KeyStoreError.missingSecret }
        let unsigned = UnsignedNostrEvent(
            pubkey: pubkey,
            createdAt: Int(now().timeIntervalSince1970),
            kind: Self.eventKind,
            tags: [["u", url.absoluteString], ["method", method.uppercased()]],
            content: ""
        )
        let event = try keyStore.sign(unsigned)
        let data = try JSONEncoder().encode(event)
        return "Nostr " + data.base64EncodedString()
    }
}
