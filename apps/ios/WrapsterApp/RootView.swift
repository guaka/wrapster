import SwiftUI
import UIKit

struct RootView: View {
    @ObservedObject var model: AppModel

    var body: some View {
        TabView {
            MediaSearchView(model: model)
                .tabItem { Label("Media", systemImage: "play.rectangle") }
            DiscoveryView(model: model)
                .tabItem { Label("Discover", systemImage: "antenna.radiowaves.left.and.right") }
            SettingsView(model: model)
                .tabItem { Label("Settings", systemImage: "gearshape") }
        }
        .sheet(item: $model.playbackRequest) { request in
            PlayerSheet(url: request.url, authorizationHeader: request.authorizationHeader)
                .ignoresSafeArea()
        }
    }
}

private struct MediaSearchView: View {
    @ObservedObject var model: AppModel

    var body: some View {
        NavigationStack {
            VStack(spacing: 0) {
                VStack(spacing: 12) {
                    Picker("Service", selection: $model.selectedService) {
                        ForEach(MediaService.allCases) { service in
                            Text(service.displayName).tag(service)
                        }
                    }
                    .pickerStyle(.segmented)

                    HStack(spacing: 10) {
                        TextField("Search Plex or Jellyfin", text: $model.searchQuery)
                            .textInputAutocapitalization(.never)
                            .submitLabel(.search)
                            .onSubmit { Task { await model.search() } }
                            .padding(.horizontal, 12)
                            .frame(height: 44)
                            .background(Color(.secondarySystemBackground), in: RoundedRectangle(cornerRadius: 8))
                        Button {
                            Task { await model.search() }
                        } label: {
                            Image(systemName: "magnifyingglass")
                                .frame(width: 44, height: 44)
                        }
                        .buttonStyle(.borderedProminent)
                        .disabled(model.isWorking || !model.hasKey)
                    }

                    Button {
                        Task { await model.refreshStatus() }
                    } label: {
                        Label("Check Gateway", systemImage: "checkmark.shield")
                            .frame(maxWidth: .infinity)
                    }
                    .buttonStyle(.bordered)
                    .disabled(model.isWorking || !model.hasKey)
                }
                .padding()

                if !model.hasKey {
                    ContentUnavailableView("Add a Nostr key", systemImage: "key", description: Text("Generate or import a key in Settings before searching media."))
                } else if model.results.isEmpty {
                    ContentUnavailableView("Search media", systemImage: "play.rectangle", description: Text("Search your authorized Wrapster Plex or Jellyfin gateway."))
                } else {
                    List(model.results) { item in
                        MediaItemRow(item: item) { model.play(item) }
                    }
                    .listStyle(.plain)
                }
            }
            .navigationTitle("Wrapster")
            .toolbar {
                if model.isWorking { ProgressView() }
            }
            .safeAreaInset(edge: .bottom) {
                StatusFooter(message: model.statusMessage)
            }
        }
    }
}

private struct MediaItemRow: View {
    let item: MediaItem
    let play: () -> Void

    var body: some View {
        HStack(spacing: 12) {
            Image(systemName: item.type.lowercased().contains("audio") ? "music.note" : "film")
                .font(.title2)
                .foregroundStyle(.teal)
                .frame(width: 38)
            VStack(alignment: .leading, spacing: 4) {
                Text(item.name)
                    .font(.headline)
                    .lineLimit(2)
                Text(item.type)
                    .font(.subheadline)
                    .foregroundStyle(.secondary)
                if let summary = item.summary, !summary.isEmpty {
                    Text(summary)
                        .font(.caption)
                        .foregroundStyle(.secondary)
                        .lineLimit(3)
                }
            }
            Spacer(minLength: 8)
            Button(action: play) {
                Image(systemName: "play.fill")
                    .frame(width: 36, height: 36)
            }
            .buttonStyle(.borderedProminent)
            .disabled(item.streamID == nil)
        }
        .padding(.vertical, 6)
    }
}

private struct DiscoveryView: View {
    @ObservedObject var model: AppModel

    var body: some View {
        NavigationStack {
            List {
                Section("Relays") {
                    TextEditor(text: $model.relayListText)
                        .font(.body.monospaced())
                        .frame(minHeight: 90)
                    Button {
                        Task { await model.loadAdverts() }
                    } label: {
                        Label("Find Services", systemImage: "antenna.radiowaves.left.and.right")
                    }
                    .disabled(model.isWorking)
                }
                Section("Services") {
                    if model.adverts.isEmpty {
                        Text("No Plex or Jellyfin adverts loaded yet.")
                            .foregroundStyle(.secondary)
                    }
                    ForEach(model.adverts) { advert in
                        AdvertRow(advert: advert)
                    }
                }
            }
            .navigationTitle("Discover")
            .toolbar { if model.isWorking { ProgressView() } }
            .safeAreaInset(edge: .bottom) { StatusFooter(message: model.statusMessage) }
        }
    }
}

private struct AdvertRow: View {
    let advert: ServiceAdvert

    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            HStack {
                Text(advert.title)
                    .font(.headline)
                Spacer()
                Text(advert.service.capitalized)
                    .font(.caption.weight(.semibold))
                    .padding(.horizontal, 8)
                    .padding(.vertical, 4)
                    .background(Color.teal.opacity(0.12), in: Capsule())
            }
            Text(advert.summary)
                .font(.subheadline)
                .foregroundStyle(.secondary)
            Text("Status: \(advert.status)")
                .font(.caption)
                .foregroundStyle(.secondary)
            Text(advert.contactPubkey)
                .font(.caption.monospaced())
                .lineLimit(1)
                .truncationMode(.middle)
            HStack {
                Button("Copy npub/contact") {
                    UIPasteboard.general.string = advert.contactPubkey
                }
                Button("Copy request") {
                    UIPasteboard.general.string = advert.accessRequestMessage
                }
            }
            .buttonStyle(.bordered)
        }
        .padding(.vertical, 6)
    }
}

private struct SettingsView: View {
    @ObservedObject var model: AppModel
    @State private var confirmingRemoval = false

    var body: some View {
        NavigationStack {
            List {
                Section("Gateway") {
                    TextField("Wrapster gateway URL", text: $model.gatewayURLString)
                        .textInputAutocapitalization(.never)
                        .autocorrectionDisabled()
                        .keyboardType(.URL)
                    Text("Dev default: http://localhost:5542")
                        .font(.caption)
                        .foregroundStyle(.secondary)
                }
                Section("Nostr key") {
                    Text(model.npub)
                        .font(.caption.monospaced())
                        .textSelection(.enabled)
                    Button("Generate new key") { model.generateKey() }
                    TextField("nsec or private-key hex", text: $model.keyInput)
                        .textInputAutocapitalization(.never)
                        .autocorrectionDisabled()
                    Button("Import key") { model.importKey() }
                        .disabled(model.keyInput.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty)
                    if let error = model.keyError {
                        Text(error)
                            .foregroundStyle(.red)
                    }
                    if model.hasKey {
                        Button("Remove stored key", role: .destructive) { confirmingRemoval = true }
                    }
                }
                Section("Scope") {
                    Text("Wrapster for iOS signs NIP-98 requests and plays authorized Plex/Jellyfin streams. It does not include torrent, magnet, seeding, or download features.")
                        .font(.footnote)
                        .foregroundStyle(.secondary)
                }
            }
            .navigationTitle("Settings")
            .alert("Remove stored key?", isPresented: $confirmingRemoval) {
                Button("Cancel", role: .cancel) {}
                Button("Remove", role: .destructive) { model.removeKey() }
            } message: {
                Text("You will lose access to this Nostr identity unless you have backed up the private key elsewhere.")
            }
        }
    }
}

private struct StatusFooter: View {
    let message: String?

    var body: some View {
        if let message, !message.isEmpty {
            Text(message)
                .font(.caption)
                .foregroundStyle(.secondary)
                .frame(maxWidth: .infinity, alignment: .leading)
                .padding(.horizontal, 16)
                .padding(.vertical, 8)
                .background(.thinMaterial)
        }
    }
}
