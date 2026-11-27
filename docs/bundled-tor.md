# Bundled Tor (next alpha)

Builds from this checkout include Tor and Lyrebird. This change targets
`v0.1.0-alpha.2`; the published `v0.1.0-alpha.1` still requires a separate Tor
installation. Nothing is downloaded during profile creation, `--help`,
`--version` or chat startup. The build downloads the dependencies once and puts
them inside the user's installer, so installation does not need to reach the Tor
download website.

## Using it

For a network that allows direct Tor, create a profile, exchange invitations and
run `shadowtalk chat`. No `tor.json` or executable paths are required.

For obfs4, create a private `tor.json` in the profile directory:

```json
{
  "version": 1,
  "mode": "obfs4",
  "startup_timeout": "5m",
  "bridges": [
    "obfs4 IP:PORT FINGERPRINT cert=CERTIFICATE iat-mode=0"
  ]
}
```

The bridge above is a placeholder. Obtain working obfs4 lines using the
[Tor connection guide](tor.md). On macOS/Linux the file needs mode `0600`.
Bundling Tor does not guarantee connectivity or automatically obtain fresh bridges.

Explicit `--tor-binary` and JSON executable paths keep their existing meaning.
Resolution is **explicit flag → JSON path → bundled executable → PATH**. A missing
bundle permits source builds to use PATH; an installed but incomplete bundle
stops startup instead of silently selecting another Tor. `lyrebird_binary` may be
omitted in obfs4 mode; then Lyrebird is selected from the bundle or PATH.

When upgrading from alpha.1, existing paths remain active. Remove `tor_binary`
and `lyrebird_binary` from your configuration to use the new defaults; keep
`mode`, bridges and other settings. Profiles, OS keys, contacts and history do not
move. Tor's writable state remains under the profile's `tor/` directory.

## Installed files

`PLATFORM` below is a Go OS/architecture pair such as `darwin-arm64`,
`linux-amd64` or `windows-amd64`.

| Distribution | Runtime location |
| --- | --- |
| Portable archive / Windows installer | `bin/tor-runtime/PLATFORM/` beside `shadowtalk` / `shadowtalk.exe` |
| macOS app | `ShadowTalk.app/Contents/Resources/tor-runtime/darwin-{arm64,amd64}/` |
| macOS CLI PKG | `/usr/local/lib/shadowtalk/tor-runtime/PLATFORM/` |
| Linux DEB/RPM | `/usr/lib/shadowtalk/tor-runtime/PLATFORM/` |

Each runtime retains the upstream `tor/`, `data/` and `docs/` layout, including
required libraries and license texts. Linux debugging symbols are omitted.
`runtime.json` records upstream archive provenance and the hashes of packaged
files. Upstream default bridge configuration is not loaded: ShadowTalk continues
to create its own restricted Tor configuration.

The universal Mac app chooses the architecture of its running Go executable.
Linux sets `LD_LIBRARY_PATH` only on the Tor child so the shipped OpenSSL/libevent
libraries can be found; it does not modify the user's shell or system settings.

## Building and updating the pinned version

[`packaging/tor-bundle.json`](../packaging/tor-bundle.json) pins **Expert Bundle
16.0.3 / Tor 0.4.9.17** and SHA-256 values for our five targets. On 2026-11-27 the
upstream `sha256sums-signed-build.txt` signature was verified against the Tor
Browser Developers primary key
`EF6E286DDA85EA2A4BA7DE684E2C6E8793298290` (signing subkey `316C1FACD62D07D9`).
Each build verifies the pinned archive hash before extraction, including cached
downloads. Keep using an explicitly reviewed version, not a mutable latest URL.

From the repository root, with Python 3.13+ and working HTTPS certificate trust:

```sh
# All targets; also called by GoReleaser's before hook.
python3 packaging/tor_bundle.py

# Only Mac runtimes, followed by a local development app.
python3 packaging/tor_bundle.py --target darwin-arm64 --target darwin-amd64
python3 packaging/macos_app.py --output dist/macos-app-next/ShadowTalk.app
```

Archives are cached in ignored `bin/tor-downloads/`; prepared runtimes are in
ignored `bin/tor-runtime/`. A checksum failure stops the build. Delete only the
reported damaged cached archive and retry. If Python reports a certificate-trust
error, repair its CA installation (when Python has no CA paths on macOS, the
downloader uses `/etc/ssl/cert.pem`; the python.org macOS installer also supplies
`Install Certificates.command`); do not disable HTTPS verification.

Mac preparation applies ad-hoc signatures to staged Mach-O files: the upstream
arm64 Tor's linker signature did not pass native validation. Original cached
archives remain unchanged. Cross-built Mac archives are finalized on the macOS
packaging runner with `python3 packaging/package.py sign-macos`; that step also
updates Homebrew archive hashes. Final checksums are generated after signing and
packaging. This is not a Developer ID signature or notarization.

When updating the pin, download the release's signed checksum list, verify its
signature with the independently identified Tor signing key, update the five
archive hashes and version fields, and run native package checks. Official
references: [Expert Bundle downloads](https://download.torproject.org/tor/),
[signature verification](https://support.torproject.org/tor-browser/getting-started/verifying-tor-browser/),
[redistribution terms](https://support.torproject.org/about-tor/using-and-sharing/distributing/).
All shipped components' license texts remain under each runtime's `docs/`.
Updating Tor currently requires rebuilding ShadowTalk packages; no background
auto-updater is introduced.
