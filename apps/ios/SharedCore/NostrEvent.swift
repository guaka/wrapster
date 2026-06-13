import CryptoKit
import Foundation

typealias NostrTag = [String]

struct UnsignedNostrEvent: Codable, Equatable {
    let pubkey: String
    let createdAt: Int
    let kind: Int
    let tags: [NostrTag]
    let content: String

    enum CodingKeys: String, CodingKey {
        case pubkey
        case createdAt = "created_at"
        case kind
        case tags
        case content
    }
}

struct NostrEvent: Codable, Equatable, Identifiable {
    let id: String
    let pubkey: String
    let createdAt: Int
    let kind: Int
    let tags: [NostrTag]
    let content: String
    let sig: String

    enum CodingKeys: String, CodingKey {
        case id
        case pubkey
        case createdAt = "created_at"
        case kind
        case tags
        case content
        case sig
    }
}

enum NIP01 {
    static func eventIDHex(for event: UnsignedNostrEvent) throws -> String {
        let payload: [Any] = [0, event.pubkey, event.createdAt, event.kind, event.tags, event.content]
        let data = try JSONSerialization.data(withJSONObject: payload, options: [.withoutEscapingSlashes])
        return Hex.encode(Data(SHA256.hash(data: data)))
    }
}
