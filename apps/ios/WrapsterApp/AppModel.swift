import Foundation
import SwiftUI

@MainActor
final class AppModel: ObservableObject {
    @Published var gatewayURLString: String {
        didSet { defaults.set(gatewayURLString, forKey: gatewayKey) }
    }
    @Published var relayListText: String {
        didSet { defaults.set(relayListText, forKey: relayKey) }
    }
    @Published var keyInput = ""
    @Published var keyError: String?
    @Published var gatewayStatus: GatewayStatus?
    @Published var statusMessage: String?
    @Published var selectedService: MediaService = .jellyfin
    @Published var searchQuery = ""
    @Published var results: [MediaItem] = []
    @Published var isWorking = false
    @Published var adverts: [ServiceAdvert] = []
    @Published var playbackRequest: PlaybackRequest?

    let keyStore: NostrKeyStore
    private let defaults: UserDefaults
    private let gatewayKey = "wrapster.ios.gatewayURL"
    private let relayKey = "wrapster.ios.relays"

    init(defaults: UserDefaults = .standard, keyStore: NostrKeyStore = NostrKeyStore()) {
        self.defaults = defaults
        self.keyStore = keyStore
        self.gatewayURLString = defaults.string(forKey: gatewayKey) ?? "http://localhost:5542"
        self.relayListText = defaults.string(forKey: relayKey) ?? "wss://relay.guaka.org\nwss://nip42.trustroots.org"
    }

    var hasKey: Bool { keyStore.currentPublicKeyHex() != nil }
    var npub: String { keyStore.currentNpub() ?? "No key stored" }

    func generateKey() {
        do {
            try keyStore.generateSecret()
            keyError = nil
            objectWillChange.send()
        } catch {
            keyError = error.localizedDescription
        }
    }

    func importKey() {
        do {
            try keyStore.importSecret(keyInput)
            keyInput = ""
            keyError = nil
            objectWillChange.send()
        } catch {
            keyError = error.localizedDescription
        }
    }

    func removeKey() {
        try? keyStore.clearSecret()
        objectWillChange.send()
    }

    func refreshStatus() async {
        await run {
            gatewayStatus = try await client().status()
            statusMessage = "Connected as \(gatewayStatus?.authenticatedPubkey?.prefix(12) ?? "unknown")"
        }
    }

    func search() async {
        let query = searchQuery.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !query.isEmpty else { return }
        await run {
            let response = try await client().search(service: selectedService, query: query)
            results = response.items
            statusMessage = "Found \(response.items.count) item\(response.items.count == 1 ? "" : "s")"
        }
    }

    func play(_ item: MediaItem) {
        guard let streamID = item.streamID else {
            statusMessage = "This item has no stream id."
            return
        }
        do {
            let gateway = try client()
            let url = gateway.streamURL(service: selectedService, streamID: streamID)
            let auth = try gateway.authorizationHeader(for: url)
            playbackRequest = PlaybackRequest(url: url, authorizationHeader: auth)
        } catch {
            statusMessage = error.localizedDescription
        }
    }

    func loadAdverts() async {
        await run {
            let relayURLs = relayListText
                .split(whereSeparator: { $0.isWhitespace || $0 == "," })
                .compactMap { URL(string: String($0)) }
            adverts = await ServiceAdvertClient(relays: relayURLs).fetch()
            statusMessage = "Found \(adverts.count) service advert\(adverts.count == 1 ? "" : "s")"
        }
    }

    private func client() throws -> WrapsterGatewayClient {
        guard let url = URL(string: gatewayURLString.trimmingCharacters(in: .whitespacesAndNewlines)) else {
            throw WrapsterGatewayError.invalidURL
        }
        return WrapsterGatewayClient(baseURL: url, signer: NIP98Signer(keyStore: keyStore))
    }

    private func run(_ action: () async throws -> Void) async {
        isWorking = true
        defer { isWorking = false }
        do {
            try await action()
        } catch {
            statusMessage = error.localizedDescription
        }
    }
}
