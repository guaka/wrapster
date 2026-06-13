# Wrapster iOS

Native SwiftUI media client for Wrapster's Plex and Jellyfin gateway.

The app talks directly to Wrapster's existing `/media/api/*` routes with NIP-98
HTTP authorization signed by a local Nostr key stored in the iOS Keychain. It is
not a browser shell and does not include torrent or file-sharing features.

## Generate and build

```sh
cd apps/ios
ruby scripts/generate_xcodeproj.rb
xcodebuild -project Wrapster.xcodeproj -scheme Wrapster -sdk iphonesimulator build
xcodebuild -project Wrapster.xcodeproj -scheme WrapsterTests -sdk iphonesimulator test
```

By default the generated project uses the same pinned `nostr-sdk-ios` revision
as the Nostroots native browser. For local package development:

```sh
NR_NOSTR_SDK_LOCAL_PATH=/absolute/path/to/nostr-sdk-ios ruby scripts/generate_xcodeproj.rb
```

## V1 scope

- Configure a Wrapster gateway URL, defaulting to `http://localhost:5542`.
- Generate or import a Nostr private key.
- Sign media API requests with NIP-98.
- Check gateway/media connector status.
- Search Plex and Jellyfin.
- Play stream URLs with native iOS playback controls.
- Discover Plex/Jellyfin service adverts from Nostr relays and copy an access
  request message.

Out of scope: NIP-07 browser injection, in-app NIP-17 direct messages, torrent
support, downloads, seeding, and media-library browsing beyond search.
