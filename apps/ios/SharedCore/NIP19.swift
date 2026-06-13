import Foundation

enum NIP19Error: Error, LocalizedError {
    case invalidFormat
    case unsupportedPrefix
    case invalidChecksum
    case invalidData
    case invalidHex

    var errorDescription: String? {
        switch self {
        case .invalidFormat: return "Invalid NIP-19 value."
        case .unsupportedPrefix: return "Only nsec and npub values are supported."
        case .invalidChecksum: return "Invalid NIP-19 checksum."
        case .invalidData: return "Invalid NIP-19 data."
        case .invalidHex: return "Invalid hex value."
        }
    }
}

enum NIP19 {
    static func importSecret(_ nsec: String) throws -> String {
        let (hrp, bytes) = try decode(nsec)
        guard hrp == "nsec" else { throw NIP19Error.unsupportedPrefix }
        guard bytes.count == 32 else { throw NIP19Error.invalidData }
        return Hex.encode(Data(bytes))
    }

    static func encodeNsec(secretHex: String) throws -> String {
        guard let bytes = Hex.decode(secretHex), bytes.count == 32 else { throw NIP19Error.invalidHex }
        return try encode(hrp: "nsec", bytes: [UInt8](bytes))
    }

    static func encodeNpub(pubkeyHex: String) throws -> String {
        guard let bytes = Hex.decode(pubkeyHex), bytes.count == 32 else { throw NIP19Error.invalidHex }
        return try encode(hrp: "npub", bytes: [UInt8](bytes))
    }

    static func isValidHex(_ value: String, expectedBytes: Int) -> Bool {
        guard value.count == expectedBytes * 2 else { return false }
        return Hex.decode(value)?.count == expectedBytes
    }

    private static let charset = Array("qpzry9x8gf2tvdw0s3jn54khce6mua7l")
    private static let generator: [UInt32] = [0x3b6a57b2, 0x26508e6d, 0x1ea119fa, 0x3d4233dd, 0x2a1462b3]

    private static func decode(_ value: String) throws -> (String, [UInt8]) {
        let lower = value.lowercased()
        guard value == lower, let separator = lower.lastIndex(of: "1"), separator > lower.startIndex else {
            throw NIP19Error.invalidFormat
        }
        let hrp = String(lower[..<separator])
        let raw = lower[lower.index(after: separator)...]
        guard raw.count >= 6 else { throw NIP19Error.invalidFormat }
        let values = try raw.map { char -> UInt8 in
            guard let idx = charset.firstIndex(of: char) else { throw NIP19Error.invalidData }
            return UInt8(idx)
        }
        guard verifyChecksum(hrp: hrp, data: values) else { throw NIP19Error.invalidChecksum }
        guard let bytes = convertBits(Array(values.dropLast(6)), fromBits: 5, toBits: 8, pad: false) else {
            throw NIP19Error.invalidData
        }
        return (hrp, bytes)
    }

    private static func encode(hrp: String, bytes: [UInt8]) throws -> String {
        guard let fiveBit = convertBits(bytes, fromBits: 8, toBits: 5, pad: true) else { throw NIP19Error.invalidData }
        let checksum = createChecksum(hrp: hrp, data: fiveBit)
        return hrp + "1" + (fiveBit + checksum).map { String(charset[Int($0)]) }.joined()
    }

    private static func convertBits(_ data: [UInt8], fromBits: UInt, toBits: UInt, pad: Bool) -> [UInt8]? {
        var acc: UInt = 0
        var bits: UInt = 0
        let maxv: UInt = (1 << toBits) - 1
        let maxAcc: UInt = (1 << (fromBits + toBits - 1)) - 1
        var ret: [UInt8] = []
        for value in data {
            let v = UInt(value)
            guard v >> fromBits == 0 else { return nil }
            acc = ((acc << fromBits) | v) & maxAcc
            bits += fromBits
            while bits >= toBits {
                bits -= toBits
                ret.append(UInt8((acc >> bits) & maxv))
            }
        }
        if pad {
            if bits > 0 { ret.append(UInt8((acc << (toBits - bits)) & maxv)) }
        } else if bits >= fromBits || ((acc << (toBits - bits)) & maxv) != 0 {
            return nil
        }
        return ret
    }

    private static func hrpExpand(_ hrp: String) -> [UInt8] {
        hrp.utf8.map { $0 >> 5 } + [0] + hrp.utf8.map { $0 & 31 }
    }

    private static func polymod(_ values: [UInt8]) -> UInt32 {
        var chk: UInt32 = 1
        for value in values {
            let top = chk >> 25
            chk = (chk & 0x1ffffff) << 5 ^ UInt32(value)
            for i in 0..<5 where ((top >> UInt32(i)) & 1) == 1 {
                chk ^= generator[i]
            }
        }
        return chk
    }

    private static func verifyChecksum(hrp: String, data: [UInt8]) -> Bool {
        polymod(hrpExpand(hrp) + data) == 1
    }

    private static func createChecksum(hrp: String, data: [UInt8]) -> [UInt8] {
        let values = hrpExpand(hrp) + data
        let mod = polymod(values + Array(repeating: 0, count: 6)) ^ 1
        return (0..<6).map { UInt8((mod >> UInt32(5 * (5 - $0))) & 31) }
    }
}
