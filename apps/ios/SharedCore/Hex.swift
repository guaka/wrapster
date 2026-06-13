import Foundation

enum Hex {
    static func decode(_ hex: String) -> Data? {
        let value = hex.trimmingCharacters(in: .whitespacesAndNewlines).lowercased()
        guard value.count % 2 == 0 else { return nil }
        var data = Data()
        data.reserveCapacity(value.count / 2)
        var index = value.startIndex
        while index < value.endIndex {
            let next = value.index(index, offsetBy: 2)
            guard let byte = UInt8(value[index..<next], radix: 16) else { return nil }
            data.append(byte)
            index = next
        }
        return data
    }

    static func encode(_ data: Data) -> String {
        data.map { String(format: "%02x", $0) }.joined()
    }
}
