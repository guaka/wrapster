import Foundation

struct ServiceAdvertClient {
    var relays: [URL]
    var timeout: TimeInterval = 8

    func fetch() async -> [ServiceAdvert] {
        await withTaskGroup(of: [ServiceAdvert].self) { group in
            for relay in relays {
                group.addTask { await fetch(from: relay, timeout: timeout) }
            }
            var byID: [String: ServiceAdvert] = [:]
            for await adverts in group {
                for advert in adverts where ["jellyfin", "plex"].contains(advert.service) {
                    byID[advert.id] = advert
                }
            }
            return byID.values.sorted { $0.title.localizedCaseInsensitiveCompare($1.title) == .orderedAscending }
        }
    }

    private func fetch(from relay: URL, timeout: TimeInterval) async -> [ServiceAdvert] {
        let task = URLSession.shared.webSocketTask(with: relay)
        task.resume()
        defer { task.cancel(with: .goingAway, reason: nil) }
        let subID = "wrapster-ios-\(UUID().uuidString)"
        let request: [Any] = [
            "REQ",
            subID,
            [
                "kinds": [31388],
                "#t": ["nostr-service-advert"],
                "limit": 100
            ]
        ]
        guard let data = try? JSONSerialization.data(withJSONObject: request), let text = String(data: data, encoding: .utf8) else {
            return []
        }
        do {
            try await task.send(.string(text))
        } catch {
            return []
        }

        let deadline = Date().addingTimeInterval(timeout)
        var adverts: [ServiceAdvert] = []
        while Date() < deadline {
            do {
                let message = try await task.receive()
                guard case let .string(payload) = message else { continue }
                if let advert = Self.parseEventMessage(payload) { adverts.append(advert) }
                if payload.contains("\"EOSE\"") { break }
            } catch {
                break
            }
        }
        return adverts
    }

    static func parseEventMessage(_ payload: String) -> ServiceAdvert? {
        guard let data = payload.data(using: .utf8),
              let raw = try? JSONSerialization.jsonObject(with: data) as? [Any],
              raw.count >= 3,
              raw.first as? String == "EVENT",
              let event = raw[2] as? [String: Any] else {
            return nil
        }
        return parseEvent(event)
    }

    static func parseEvent(_ event: [String: Any]) -> ServiceAdvert? {
        guard let pubkey = event["pubkey"] as? String,
              let content = event["content"] as? String,
              let tags = event["tags"] as? [[String]] else {
            return nil
        }
        func tag(_ name: String) -> [String]? { tags.first { $0.first == name } }
        func tagValue(_ name: String) -> String? { tag(name).flatMap { $0.count > 1 ? $0[1] : nil } }
        guard let d = tagValue("d"),
              let title = tagValue("title"),
              let summary = tagValue("summary"),
              let service = tagValue("service"),
              let status = tagValue("status") else {
            return nil
        }
        let contact = tags.first { $0.first == "p" && $0.count > 3 && $0[3] == "contact" } ?? tags.first { $0.first == "p" }
        guard let contactPubkey = contact.flatMap({ $0.count > 1 ? $0[1] : nil }) else { return nil }
        let relayHint = contact.flatMap { $0.count > 2 ? $0[2] : nil }
        return ServiceAdvert(
            id: "31388:\(pubkey):\(d)",
            title: title,
            summary: summary,
            service: service,
            status: status,
            contactPubkey: contactPubkey,
            relayHint: relayHint,
            content: content
        )
    }
}
