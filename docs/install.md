# Install a packaged build

ShadowTalk is a terminal application. Published versions are available from
[GitHub Releases](https://github.com/bacrehclb/ShadowTalk/releases); choose a
version and read its alpha limitations. Until a release is published, or to
test development code, download `shadowtalk-packages` from a successful workflow
run on the intended commit. An unpublished release draft is not a public download.

The prepared `v0.1.0-alpha.1` release contains six platform downloads: one universal
macOS DMG, one Windows setup EXE and four Linux DEB/RPM packages, plus checksums
and dependency notices (eight uploaded files in total).
Download one installer for your platform. Portable archives and standalone PKGs
are available in the full `shadowtalk-packages` CI artifact, not as release assets.
GitHub's **Source code** ZIP/tarball downloads are the repository sources.

Download `checksums.txt` from the same release. For a single downloaded file,
compare `shasum -a 256 FILE` on macOS, `sha256sum FILE` on Linux, or
`Get-FileHash .\FILE -Algorithm SHA256` in PowerShell with the matching manifest
entry. `-c checksums.txt` checks every listed file and reports missing downloads
if you only downloaded one installer. A checksum detects changes to a file;
it is not a publisher signature.

## Requirements

- No Go compiler is needed to use a packaged binary.
- Builds for the upcoming alpha.2 include Tor and Lyrebird. The published alpha.1
  requires a separate [Tor Expert Bundle](https://download.torproject.org/tor/).
  Follow [the connection guide](tor.md); see [bundled Tor](bundled-tor.md) for
  automatic discovery and upgrading existing configurations.
- An accessible OS credential store: Keychain on macOS, Credential Manager on
  Windows, or an unlocked Secret Service collection over user D-Bus on Linux.
- A real terminal, at least 60 columns × 20 rows for chat.

Current targets: macOS and Linux `arm64`/`amd64`, Windows `amd64` (x64).
The macOS app launcher requires macOS 12+ and includes both architectures.
Native Windows ARM64, 32-bit systems and specific older OS versions have not
been qualified. Cross-compilation does not establish runtime compatibility.

## macOS: DMG or terminal

Download **`shadowtalk_0.1.0-alpha.1_darwin_universal.dmg`** for either Apple
Silicon or Intel. Open it, drag **ShadowTalk.app** onto **Applications**, then
launch the copied app. Eject the disk image once copying finishes.

The app opens Terminal with a Russian menu: **1 — Профиль** (profile),
**2 — Чат** (chat), **3 — Команды** (commands), **0 — Выход** (exit).
Choose **1** to create/open your profile, **3** to exchange invitations and add
contacts, and **2** to chat after configuring Tor. In the command prompt,
`exit` or Ctrl+D returns to the menu.

The app does not install a command in the system PATH. Inside its **Команды**
prompt, use `shadowtalk --version` and all README examples normally. From another
terminal, use the bundled executable's full path:

```sh
"/Applications/ShadowTalk.app/Contents/MacOS/shadowtalk" --version
"/Applications/ShadowTalk.app/Contents/MacOS/shadowtalk" chat
```

If you installed the app elsewhere, substitute that location. Profiles,
credentials and Tor settings use the same locations as the standalone CLI.
To remove the app, move that copy of `ShadowTalk.app` to the Trash; profile and
Keychain data remain intact. See [the app guide](macos-app.md) for local builds.

The app has an **ad-hoc code signature**, but no Developer ID signature or Apple
notarization. macOS may block downloaded copies. Local signature verification
does not establish a verified publisher; this workflow does not disable
Gatekeeper or remove quarantine attributes.

**Alternative CLI installation:** the complete CI artifact contains separate
`darwin_arm64` and `darwin_amd64` PKGs and portable `.tar.gz` archives. A PKG
requires administrator access and installs `/usr/local/bin/shadowtalk` with
documentation in `/usr/local/share/doc/shadowtalk`. A portable archive can be
extracted and run with `./shadowtalk --version`. The public universal DMG contains
the app rather than a PKG.

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
a keyring provider. Use your distribution's package manager for a Secret Service
provider if needed; an unlocked desktop login session is the simplest starting point.
Remove with `sudo apt remove shadowtalk` or `sudo dnf remove shadowtalk`.
Profiles and OS credentials are outside the package and remain intact.

The `.tar.gz` in the CI artifact provides a portable binary for other distributions. Extract it
and run `./shadowtalk --version`. A hosted APT/RPM repository is a separate
distribution step; `apt install shadowtalk` alone will not find this project.

## First launch

For the macOS app, choose **3 — Команды** before running these commands, or use
**1 — Профиль** to open the profile screen. For other installations, use your terminal.

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

- macOS app: `ShadowTalk.app/Contents/Resources/Documentation/`.
- Alternative macOS PKG: `/usr/local/share/doc/shadowtalk/`.
- Windows setup: the installation directory, normally
  `%LOCALAPPDATA%\Programs\ShadowTalk\`.
- Linux DEB/RPM: `/usr/share/doc/shadowtalk/`.

For alpha.2 builds, Tor and Lyrebird license texts are included under each
bundled runtime's `docs/` directory; see [runtime locations](bundled-tor.md#installed-files).
Alpha.1 uses a separate Tor installation with its own license texts.
