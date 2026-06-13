import XCTest
@testable import Wrapster

final class WrapsterTests: XCTestCase {
    let secretHex = "0000000000000000000000000000000000000000000000000000000000000001"

    func testKeyImportAcceptsHexAndNsec() throws {
        XCTAssertEqual(try NostrKeyStore.parseSecret(secretHex), secretHex)
        let nsec = try NIP19.encodeNsec(secretHex: secretHex)
        XCTAssertEqual(try NostrKeyStore.parseSecret(nsec), secretHex)
        XCTAssertThrowsError(try NostrKeyStore.parseSecret("npub1example"))
    }

    func testNIP98HeaderContainsRequestURLAndMethod() throws {
        let store = InMemoryTestKeyStore(secretHex: secretHex)
        let signer = NIP98Signer(keyStore: store, now: { Date(timeIntervalSince1970: 1_800_000_000) })
        let url = URL(string: "https://wrapster.test/media/api/status")!

        let header = try signer.authorizationHeader(url: url, method: "get")
        let event = try decodeAuthorization(header)

        XCTAssertEqual(event.kind, 27235)
        XCTAssertEqual(event.createdAt, 1_800_000_000)
        XCTAssertEqual(event.tags, [["u", url.absoluteString], ["method", "GET"]])
        XCTAssertEqual(event.pubkey, InMemoryTestKeyStore.pubkey)
        XCTAssertFalse(event.sig.isEmpty)
    }

    func testGatewayURLNormalizationAndStreamURL() throws {
        let store = InMemoryTestKeyStore(secretHex: secretHex)
        let gateway = WrapsterGatewayClient(
            baseURL: URL(string: "https://wrapster.test/base/?ignored=1")!,
            signer: NIP98Signer(keyStore: store)
        )

        XCTAssertEqual(gateway.baseURL.absoluteString, "https://wrapster.test/base")
        XCTAssertEqual(
            gateway.streamURL(service: .jellyfin, streamID: "item123").absoluteString,
            "https://wrapster.test/base/media/api/services/jellyfin/stream/item123"
        )
    }

    func testDecodesMediaSearchResponse() throws {
        let json = #"{"service":"plex","items":[{"id":"1","name":"A Movie","type":"movie","summary":"Nice","stream_id":"abc"}]}"#.data(using: .utf8)!
        let response = try JSONDecoder().decode(MediaSearchResponse.self, from: json)

        XCTAssertEqual(response.service, .plex)
        XCTAssertEqual(response.items.first?.name, "A Movie")
        XCTAssertEqual(response.items.first?.streamID, "abc")
    }

    func testParsesServiceAdvertEvent() throws {
        let event: [String: Any] = [
            "pubkey": InMemoryTestKeyStore.pubkey,
            "content": "A tiny Jellyfin.",
            "tags": [
                ["d", "jellyfin:tiny"],
                ["title", "Tiny Jellyfin"],
                ["summary", "Small media server"],
                ["service", "jellyfin"],
                ["status", "active"],
                ["p", InMemoryTestKeyStore.pubkey, "wss://relay.example", "contact"],
                ["t", "nostr-service-advert"],
                ["t", "service:jellyfin"]
            ]
        ]

        let advert = try XCTUnwrap(ServiceAdvertClient.parseEvent(event))
        XCTAssertEqual(advert.id, "31388:\(InMemoryTestKeyStore.pubkey):jellyfin:tiny")
        XCTAssertEqual(advert.title, "Tiny Jellyfin")
        XCTAssertEqual(advert.relayHint, "wss://relay.example")
        XCTAssertTrue(advert.accessRequestMessage.contains("jellyfin"))
    }

    private func decodeAuthorization(_ header: String) throws -> NostrEvent {
        let prefix = "Nostr "
        XCTAssertTrue(header.hasPrefix(prefix))
        let encoded = String(header.dropFirst(prefix.count))
        let data = try XCTUnwrap(Data(base64Encoded: encoded))
        return try JSONDecoder().decode(NostrEvent.self, from: data)
    }
}

private final class InMemoryTestKeyStore: NostrKeyStoring {
    static let pubkey = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
    var secretHex: String?
    let provider = StubCryptoProvider(pubkey: pubkey)

    init(secretHex: String?) {
        self.secretHex = secretHex
    }

    func importSecret(_ raw: String) throws { secretHex = try NostrKeyStore.parseSecret(raw) }
    func generateSecret() throws { secretHex = String(repeating: "1", count: 64) }
    func clearSecret() throws { secretHex = nil }
    func currentSecretHex() -> String? { secretHex }
    func currentPublicKeyHex() -> String? { secretHex == nil ? nil : Self.pubkey }
    func currentNpub() -> String? { try? NIP19.encodeNpub(pubkeyHex: Self.pubkey) }

    func sign(_ unsigned: UnsignedNostrEvent) throws -> NostrEvent {
        guard let secretHex else { throw KeyStoreError.missingSecret }
        let eventID = try NIP01.eventIDHex(for: unsigned)
        return NostrEvent(
            id: eventID,
            pubkey: unsigned.pubkey,
            createdAt: unsigned.createdAt,
            kind: unsigned.kind,
            tags: unsigned.tags,
            content: unsigned.content,
            sig: try provider.signEventIDHex(eventID, secretHex: secretHex)
        )
    }
}
