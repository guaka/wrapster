import CryptoKit
import Foundation
#if canImport(NostrSDK)
import NostrSDK
#endif

enum KeyStoreError: Error, LocalizedError {
    case missingSecret
    case invalidInput
    case signingFailed

    var errorDescription: String? {
        switch self {
        case .missingSecret: return "No private key is stored on this device."
        case .invalidInput: return "Unable to import this Nostr key."
        case .signingFailed: return "Unable to sign this request."
        }
    }
}

enum NostrCryptoProviderError: Error {
    case invalidKeyMaterial
    case unsupported(String)
}

protocol NostrCryptoProviding {
    var algorithmLabel: String { get }
    func publicKeyHex(fromSecretHex secretHex: String) throws -> String
    func signEventIDHex(_ eventID: String, secretHex: String) throws -> String
}

#if canImport(NostrSDK)
struct NostrSDKCryptoProvider: NostrCryptoProviding, ContentSigning {
    let algorithmLabel = "nostr-sdk-ios-secp256k1-schnorr"

    func publicKeyHex(fromSecretHex secretHex: String) throws -> String {
        guard let keypair = NostrSDK.Keypair(hex: secretHex) else {
            throw NostrCryptoProviderError.invalidKeyMaterial
        }
        return keypair.publicKey.hex
    }

    func signEventIDHex(_ eventID: String, secretHex: String) throws -> String {
        guard Hex.decode(eventID)?.count == 32, NostrSDK.PrivateKey(hex: secretHex) != nil else {
            throw NostrCryptoProviderError.invalidKeyMaterial
        }
        return try signatureForContent(eventID, privateKey: secretHex)
    }
}
#else
struct NostrSDKCryptoProvider: NostrCryptoProviding {
    let algorithmLabel = "nostr-sdk-ios-unavailable"

    func publicKeyHex(fromSecretHex secretHex: String) throws -> String {
        _ = secretHex
        throw NostrCryptoProviderError.unsupported("NostrSDK package is not linked in this build.")
    }

    func signEventIDHex(_ eventID: String, secretHex: String) throws -> String {
        _ = eventID
        _ = secretHex
        throw NostrCryptoProviderError.unsupported("NostrSDK package is not linked in this build.")
    }
}
#endif

struct StubCryptoProvider: NostrCryptoProviding {
    let algorithmLabel = "stub"
    var pubkey = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

    func publicKeyHex(fromSecretHex secretHex: String) throws -> String {
        guard NIP19.isValidHex(secretHex, expectedBytes: 32) else { throw NostrCryptoProviderError.invalidKeyMaterial }
        return pubkey
    }

    func signEventIDHex(_ eventID: String, secretHex: String) throws -> String {
        guard NIP19.isValidHex(secretHex, expectedBytes: 32), NIP19.isValidHex(eventID, expectedBytes: 32) else {
            throw NostrCryptoProviderError.invalidKeyMaterial
        }
        let mac = HMAC<SHA512>.authenticationCode(for: Data(eventID.utf8), using: SymmetricKey(data: Data(secretHex.utf8)))
        return Hex.encode(Data(mac))
    }
}
