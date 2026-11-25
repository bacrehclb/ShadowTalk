# Install a packaged build

ShadowTalk is a terminal application. Published versions are available from
[GitHub Releases](https://github.com/bacrehclb/ShadowTalk/releases); choose a
version and read its alpha limitations. Until a release is published, or to
test development code, download `shadowtalk-packages` from a successful workflow
run on the intended commit. An unpublished release draft is not a public download.

A public release contains seven installers: two macOS DMGs, one Windows setup
EXE and four Linux DEB/RPM packages, plus checksums and dependency notices.
Download one installer for your platform. Portable archives and standalone PKGs
are available in the full `shadowtalk-packages` CI artifact, not as release assets.
GitHub's **Source code** ZIP/tarball downloads are the repository sources.

The artifact contains a `checksums.txt` SHA-256 manifest. On macOS use
`shasum -a 256 -c checksums.txt`; on Linux use `sha256sum -c checksums.txt`.
On Windows, compare `Get-FileHash .\FILE -Algorithm SHA256` with its manifest
entry. A checksum detects changes to a file; it is not a publisher signature.

## Requirements

- No Go compiler is needed to use a packaged binary.
- Tor is installed separately. For obfs4, install Lyrebird as well. Use the
  [Tor Expert Bundle](https://download.torproject.org/tor/) for your OS/CPU and
  follow [the connection guide](tor.md). Keep the bundle's supporting libraries.
- An accessible OS credential store: Keychain on macOS, Credential Manager on
  Windows, or an unlocked Secret Service collection over user D-Bus on Linux.
- A real terminal, at least 60 columns × 20 rows for chat.

Current targets: macOS and Linux `arm64`/`amd64`, Windows `amd64` (x64).
Native Windows ARM64, 32-bit systems and specific older OS versions have not
been qualified. Cross-compilation does not establish runtime compatibility.

## macOS: DMG or terminal

Choose `darwin_arm64` for Apple Silicon or `darwin_amd64` for Intel.

**Graphical installation:** open the `.dmg`, then `Install ShadowTalk.pkg`.
The native Installer puts the command in `/usr/local/bin/shadowtalk` and
documentation in `/usr/local/share/doc/shadowtalk`. It requires administrator
access and does not create a desktop `.app` or modify shell configuration.

**Terminal installation:** download the matching `.dmg`, verify its checksum,
then substitute its exact filename:

```sh
hdiutil attach ./shadowtalk_VERSION_darwin_ARCH.dmg
sudo installer -pkg "/Volumes/ShadowTalk/Install ShadowTalk.pkg" -target /
hdiutil detach "/Volumes/ShadowTalk"
shadowtalk --version
```

Use the volume path reported by `hdiutil` if it differs. The CI artifact also
contains a standalone PKG and a portable `.tar.gz`; the latter can be unpacked
and run with `./shadowtalk --version`. Install only one copy into a given command
location.

The current development builds are **not Developer ID signed or notarized**.
macOS may block downloaded packages. Public distribution with publisher trust
requires signing/notarization before packaging and checksum generation; this
workflow does not disable Gatekeeper or remove quarantine attributes.

GoReleaser also generates a Homebrew cask recipe. A public tap is not available
yet, so `brew install shadowtalk` is not an advertised installation command.
Snapshot casks are build output only.

To remove a PKG installation, remove `/usr/local/bin/shadowtalk` and
`/usr/local/share/doc/shadowtalk`, then forget its receipt with
`sudo pkgutil --forget io.github.bacrehclb.shadowtalk`. Only do this for a
PKG-managed copy; it preserves the profile and Keychain entries.

## Windows: setup wizard

Run `shadowtalk_VERSION_windows_amd64_setup.exe`. It installs for the current
user in `%LOCALAPPDATA%\Programs\ShadowTalk` without requesting administrator
rights. The optional PATH task makes `shadowtalk` available in **new terminals**.
The Start menu shortcut opens a terminal with command help. The wizard uses
Inno Setup's modern layout and supports English and Russian.

```powershell
shadowtalk --version
shadowtalk ui
```

The CI artifact also contains a portable ZIP: extract it, open PowerShell in that
folder, and run `.\shadowtalk.exe --version`. Current installers are not Authenticode
signed, so Windows may show an unknown-publisher warning.

Uninstall through Windows Settings or the Start menu entry. The uninstaller
removes program files and only the PATH entry added by the installer; profiles,
messages and credentials are retained.

## Linux: DEB or RPM package

Linux supports both package files and command-line installation. Download the
correct architecture and substitute the exact filename:

```sh
# Debian / Ubuntu
sudo apt install ./shadowtalk_VERSION_linux_ARCH.deb

# Fedora / RPM-based systems using DNF
sudo dnf install ./shadowtalk_VERSION_linux_ARCH.rpm

shadowtalk --version
```

Both install `/usr/bin/shadowtalk` with documentation under
`/usr/share/doc/shadowtalk`. They do not configure a service, start Tor or install
a keyring provider. Use your distribution's package manager for those runtime
dependencies; an unlocked desktop login session is the simplest starting point.
Remove with `sudo apt remove shadowtalk` or `sudo dnf remove shadowtalk`.
Profiles and OS credentials are outside the package and remain intact.

The `.tar.gz` in the CI artifact provides a portable binary for other distributions. Extract it
and run `./shadowtalk --version`. A hosted APT/RPM repository is a separate
distribution step; `apt install shadowtalk` alone will not find this project.

## First launch

```sh
shadowtalk --version
shadowtalk ui
```

Create one profile per device, configure Tor, and exchange invitations in both
directions. Follow the [English README](../README.md#usage) or
[Russian README](../README.ru.md#использование) for contact verification and chat.

Package installation does not validate live messaging. The current live test
covered two independent nodes on one Mac; two-device and live Windows/Linux
checks remain outstanding. See [the test report](testing/tor-live-exchange.md).

## Licenses

`LICENSE` contains ShadowTalk's MIT license. `THIRD_PARTY_NOTICES.txt` contains
the bundled Go dependency/toolchain notices and is also a separate release
download. Find both files in the extracted portable archive, or after installing:

- macOS PKG/DMG: `/usr/local/share/doc/shadowtalk/`.
- Windows setup: the installation directory, normally
  `%LOCALAPPDATA%\Programs\ShadowTalk\`.
- Linux DEB/RPM: `/usr/share/doc/shadowtalk/`.

Tor and Lyrebird are installed separately and provide their own license texts.
