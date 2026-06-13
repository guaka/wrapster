import AVFoundation
import AVKit
import Foundation
import SwiftUI

struct PlayerSheet: UIViewControllerRepresentable {
    let url: URL
    let authorizationHeader: String

    func makeUIViewController(context: Context) -> AVPlayerViewController {
        let asset = AVURLAsset(
            url: url,
            options: ["AVURLAssetHTTPHeaderFieldsKey": ["Authorization": authorizationHeader]]
        )
        let item = AVPlayerItem(asset: asset)
        let controller = AVPlayerViewController()
        controller.player = AVPlayer(playerItem: item)
        controller.player?.play()
        return controller
    }

    func updateUIViewController(_ controller: AVPlayerViewController, context: Context) {}
}

struct PlaybackRequest: Identifiable, Equatable {
    let id = UUID()
    let url: URL
    let authorizationHeader: String
}
