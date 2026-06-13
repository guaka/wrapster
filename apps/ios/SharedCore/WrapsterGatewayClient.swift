import Foundation

struct WrapsterGatewayClient {
    let baseURL: URL
    let signer: NIP98Signer
    var session: URLSession = .shared

    init(baseURL: URL, signer: NIP98Signer, session: URLSession = .shared) {
        self.baseURL = Self.normalizedBaseURL(baseURL)
        self.signer = signer
        self.session = session
    }

    static func normalizedBaseURL(_ url: URL) -> URL {
        guard var comps = URLComponents(url: url, resolvingAgainstBaseURL: false) else { return url }
        let trimmedPath = comps.path.trimmingCharacters(in: CharacterSet(charactersIn: "/"))
        comps.path = trimmedPath.isEmpty ? "" : "/" + trimmedPath
        comps.query = nil
        comps.fragment = nil
        return comps.url ?? url
    }

    func status() async throws -> GatewayStatus {
        try await getJSON(path: "/media/api/status", queryItems: [])
    }

    func search(service: MediaService, query: String) async throws -> MediaSearchResponse {
        try await getJSON(
            path: "/media/api/services/\(service.rawValue)/search",
            queryItems: [URLQueryItem(name: "q", value: query)]
        )
    }

    func streamURL(service: MediaService, streamID: String) -> URL {
        baseURL.appendingPathComponent("media/api/services/\(service.rawValue)/stream/\(streamID)")
    }

    func authorizationHeader(for url: URL, method: String = "GET") throws -> String {
        try signer.authorizationHeader(url: url, method: method)
    }

    private func getJSON<T: Decodable>(path: String, queryItems: [URLQueryItem]) async throws -> T {
        let url = try endpoint(path: path, queryItems: queryItems)
        var request = URLRequest(url: url)
        request.httpMethod = "GET"
        request.setValue(try signer.authorizationHeader(url: url, method: "GET"), forHTTPHeaderField: "Authorization")
        let (data, response) = try await session.data(for: request)
        if let http = response as? HTTPURLResponse, !(200..<300).contains(http.statusCode) {
            throw WrapsterGatewayError.httpStatus(http.statusCode, String(data: data, encoding: .utf8) ?? "")
        }
        return try JSONDecoder().decode(T.self, from: data)
    }

    private func endpoint(path: String, queryItems: [URLQueryItem]) throws -> URL {
        let cleanPath = path.hasPrefix("/") ? String(path.dropFirst()) : path
        let url = baseURL.appendingPathComponent(cleanPath)
        guard var comps = URLComponents(url: url, resolvingAgainstBaseURL: false) else { throw WrapsterGatewayError.invalidURL }
        comps.queryItems = queryItems.isEmpty ? nil : queryItems
        guard let final = comps.url else { throw WrapsterGatewayError.invalidURL }
        return final
    }
}

enum WrapsterGatewayError: Error, LocalizedError, Equatable {
    case invalidURL
    case httpStatus(Int, String)

    var errorDescription: String? {
        switch self {
        case .invalidURL: return "Invalid Wrapster gateway URL."
        case let .httpStatus(status, body): return "Wrapster returned HTTP \(status). \(body)"
        }
    }
}
