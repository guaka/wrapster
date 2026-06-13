import Foundation

enum MediaService: String, CaseIterable, Identifiable, Codable {
    case jellyfin
    case plex

    var id: String { rawValue }
    var displayName: String { rawValue.prefix(1).uppercased() + rawValue.dropFirst() }
}

struct MediaItem: Codable, Identifiable, Equatable {
    let id: String
    let name: String
    let type: String
    let summary: String?
    let streamID: String?

    enum CodingKeys: String, CodingKey {
        case id
        case name
        case type
        case summary
        case streamID = "stream_id"
    }
}

struct MediaSearchResponse: Codable, Equatable {
    let service: MediaService
    let items: [MediaItem]
}

struct GatewayStatus: Codable, Equatable {
    let authenticatedPubkey: String?
    let transport: String?
    let connector: ConnectorStatus?

    enum CodingKeys: String, CodingKey {
        case authenticatedPubkey = "authenticated_pubkey"
        case transport
        case connector
    }
}

struct ConnectorStatus: Codable, Equatable {
    let services: [String: ServiceConfiguration]
}

struct ServiceConfiguration: Codable, Equatable {
    let configured: Bool
}

struct ServiceAdvert: Identifiable, Equatable {
    let id: String
    let title: String
    let summary: String
    let service: String
    let status: String
    let contactPubkey: String
    let relayHint: String?
    let content: String

    var accessRequestMessage: String {
        "Hi, I found your \(service) service advert on Nostr. I would like to request access.\n\nMy profile/community context:\n"
    }
}
