<div align="center">

  <h1>ShadowTalk</h1>

  <p>
    <b>A peer-to-peer messenger for your terminal.</b><br>
    Built in Go. Connects through Tor. Identity through cryptographic keys.
  </p>

  <p>
    <a href="https://go.dev/dl/"><img src="https://img.shields.io/badge/Go-1.26%2B-00ADD8?style=flat-square&logo=go&logoColor=white" alt="Go 1.26 or newer"></a>
    <a href="https://github.com/bacrehclb/ShadowTalk/actions/workflows/test.yml?query=branch%3Amain"><img src="https://img.shields.io/github/actions/workflow/status/bacrehclb/ShadowTalk/test.yml?branch=main&event=push&style=flat-square&label=tests%20%28main%29" alt="Tests on main"></a>
    <a href="LICENSE"><img src="https://img.shields.io/badge/License-MIT-green?style=flat-square" alt="MIT License"></a>
    <br>
    <img src="https://img.shields.io/badge/Platforms-Windows%20%7C%20macOS%20%7C%20Linux-64748b?style=flat-square" alt="Windows, macOS and Linux">
    <img src="https://img.shields.io/badge/Status-experimental-d97706?style=flat-square" alt="Experimental">
  </p>

  <p><b>English</b> · <a href="README.ru.md">Русский</a></p>
  <p>
    <a href="#quick-start">Quick start</a> ·
    <a href="#usage">Usage</a> ·
    <a href="#roadmap">Roadmap</a> ·
    <a href="#documentation">Documentation</a>
  </p>

</div>

---

## Overview

ShadowTalk is an open-source terminal messenger written in Go. Create a local identity, exchange invitations with someone you trust, and communicate through Tor onion services without a shared ShadowTalk server. A nickname is a display name; your cryptographic key is your identity.

> **Experimental alpha:** one-to-one terminal chat, encrypted message history and delivery retries are implemented. Commands and storage formats may change between alpha versions. There has been no independent security audit.
>
> Active development happens on **[dev](https://github.com/bacrehclb/ShadowTalk/tree/dev)**. For a released version, use its tag, release notes and bundled documentation.

## What works today

| Feature | Available behavior |
| --- | --- |
| Local identity | Ed25519 keys, a full SHA-256 fingerprint and a persistent profile |
| Key storage | AES-256-GCM encrypted seed; encryption key stored in the OS credential store |
| Terminal UI | Profile management, contact selection, message editor, paginated history and delivery status |
| Invitations | Signed tokens with an onion address, public key and expiration time |
| Contacts | Local aliases, explicit fingerprint confirmation and persistent storage |
| Secure channel | TLS 1.3 with mutual verification of confirmed identity keys |
| Tor transport | Managed Tor process and stable v3 onion address |
| History | Encrypted message text, duplicate detection and paginated history |
| Delivery | Persistent outgoing queue, acknowledgement after saving, retries after restart and pending/delivered states |
| Distribution | macOS DMG, Windows setup EXE and Linux DEB/RPM installers |

Live delivery and restart recovery were verified through the public Tor network using obfs4 with two nodes on one macOS arm64 computer. Separate-device exchange and live Windows/Linux networking remain unverified. See the [live test report](docs/testing/tor-live-exchange.md) for the exact scope.

## Quick start

### Packaged builds

Choose a published version from [GitHub Releases](https://github.com/bacrehclb/ShadowTalk/releases)
and follow the [installation guide](docs/install.md). If no release is available,
use the `shadowtalk-packages` artifact from a successful
[Packages run](https://github.com/bacrehclb/ShadowTalk/actions/workflows/packages.yml?query=branch%3Adev)
on the intended commit. Read the version's limitations before installing.

| Platform | Architectures | Downloads |
| --- | --- | --- |
| macOS | Apple Silicon (`arm64`), Intel (`amd64`) | `.dmg` (contains the PKG installer) |
| Windows | x64 (`amd64`) | `_setup.exe` |
| Linux | `arm64`, `amd64` | `.deb`, `.rpm` |

Download one installer for your OS and processor. Portable archives, standalone
PKGs and maintainer files remain in the `shadowtalk-packages` CI artifact.

Packages include a dependency notice file and are distributed with SHA-256
checksums. Current builds are unsigned; macOS builds are not notarized.
Go is only needed to [build from source](#development).

### Requirements

- **Tor** for chat; **Lyrebird** as well when using obfs4 bridges. Install these separately and follow the [connection guide](docs/tor.md).
- **An accessible OS credential store:** Keychain on macOS, Credential Manager on Windows, or Secret Service over a user D-Bus session on Linux. Linux needs a running secret service with an available login/default collection; D-Bus alone is not enough.
- **An interactive terminal:** at least 40 × 20 for the profile UI and 60 × 20 for chat.

### Create your profile

After installation, open a terminal and run:

```sh
shadowtalk --version
shadowtalk ui
```

The examples below use `shadowtalk` on PATH. For a portable archive, use
`./shadowtalk` on macOS/Linux or `.\shadowtalk.exe` in PowerShell from the
extracted directory. For a source build, use the binary path shown under
[Development](#development). Profile and contact commands work without Tor.

Enter a nickname and press **Enter** to create your profile. On subsequent launches, ShadowTalk loads the same identity. Your OS may ask you to unlock or allow access to its credential store.

Nicknames contain **3–24 characters**: Latin letters, digits, `_` and `-`, with a letter or digit at each end. They are case-sensitive and do not need to be globally unique.

| Key | Action |
| --- | --- |
| Enter | Confirm the action shown on screen |
| `r` | Reload the profile; while entering a nickname, type the letter normally |
| Esc / Ctrl+C | Exit after any active key operation finishes |

The interface and command output are currently in Russian. The TUI requires terminal stdin and stdout; use the regular commands below in scripts. A nonempty `NO_COLOR` disables colors.

## Usage

### Manage your profile

If you have not created a profile through the TUI:

```sh
shadowtalk profile create --nick alice_01
```

View your profile and public-key fingerprint:

```sh
shadowtalk profile show
```

Creating a profile does not overwrite an existing one. `profile upgrade` adds keys to an older profile that only contains metadata. `profile recover` handles interrupted creation; it cannot restore a lost OS encryption key.

### Exchange invitations

Create an invitation and send the resulting file to your intended contact:

```sh
shadowtalk invite create --ttl 24h > alice.invite
```

In Windows PowerShell, write the token as ASCII to avoid an encoding marker:

```powershell
shadowtalk invite create --ttl 24h | Set-Content -Encoding ascii alice.invite
```

The recipient inspects the invitation:

```sh
shadowtalk invite inspect --file alice.invite
```

**Compare the full fingerprint with the sender through a trusted channel.** A valid signature proves ownership of a key, not the identity of the person sending you the file. After checking, replace `SHA256:...` below with the full confirmed fingerprint:

```sh
shadowtalk contacts add --alias alice --fingerprint SHA256:... --file alice.invite
shadowtalk contacts list
shadowtalk contacts show --alias alice
```

Each participant needs their own profile. Exchange invitations in both directions to prepare mutual trust. These commands only save local contacts; they do not contact the other person or open a chat.

- Invitations expire after **24 hours** by default; `--ttl` accepts whole seconds from `1s` to `168h`.
- Tokens contain public identity data, including the onion address, but no private keys. Share them deliberately.
- Tokens can be reused until they expire. Single-use acceptance and revocation are not implemented. Saved contacts remain after token expiry.
- `--file -` reads one token from stdin until EOF. Results go to stdout, errors to stderr; failures return exit code `1`.

Remove a local contact by confirming its stored fingerprint:

```sh
shadowtalk contacts remove --alias alice --fingerprint SHA256:...
```

This removes the local entry; it does not revoke invitations. For command details, use `--help`, `invite --help` or `contacts --help`.

### Open a chat

After both participants have saved each other's confirmed invitations, install Tor separately and run:

```sh
shadowtalk chat --contact alice
```

If Tor is not on PATH, specify its executable (including `tor.exe` on Windows):

```sh
shadowtalk chat --tor-binary "/path/to/tor" --startup-timeout 5m
```

The screen shows Tor's bootstrap percentage and stage, then contacts, history and the message editor. The default Tor startup timeout is 3 minutes; `--startup-timeout` accepts 1 second to 15 minutes. History opens after Tor is ready. Omitting `--contact` selects the first saved contact. Contacts and invitations are managed using the commands above; restart chat to apply changes.

Connection settings can be saved in `tor.json` inside the profile directory, or selected with `chat --tor-config "/path/to/tor.json"`. Explicit `--tor-binary` and `--startup-timeout` flags override the file. A missing default file uses direct Tor; an invalid file stops startup. Retrying with Enter / `r` reloads the file.

For **obfs4 bridges**, configure a separate Lyrebird executable and your bridge lines. Tor and Lyrebird are available in the [Tor Expert Bundle](https://support.torproject.org/little-t-tor/circumvention/using-bridges/). See [connection configuration](docs/tor.md#конфигурация-подключения) for the JSON format and setup. Bridge mode never silently falls back to direct Tor. Live delivery and restart recovery have been verified with obfs4 on macOS; bridge availability depends on your network.

| Key | Chat action |
| --- | --- |
| Tab | Switch between contacts and message input |
| ↑ / ↓ in contacts | Select a conversation |
| Enter | Open the selected conversation or queue the message |
| Alt+Enter | Insert a newline |
| PgUp / PgDn | Scroll history; PgUp at the top loads older records |
| Ctrl+L | Return to the latest messages and statuses |
| Ctrl+R after a save error | Retry the same message without changing its ID |
| Esc / Ctrl+C | Stop networking and exit |

A `•` beside a contact marks changes during this session. It is not a read receipt. **Pending** messages stay in the encrypted local queue; **delivered** means the peer acknowledged saving them. Both users must be online for delivery. Saved messages resume automatically on the next `chat` launch; unsaved drafts are kept only in memory. The screen keeps up to 200 history records at a time; the database retains older records.

Message text is limited to 16384 UTF-8 bytes. Oversized pastes are rejected, and terminal control sequences are displayed safely. `chat` requires terminal input and output; it does not accept piped messages. Details: `chat --help`.

## Local data and security

| OS | Default profile directory |
| --- | --- |
| macOS | `~/Library/Application Support/shadowtalk` |
| Linux | `$XDG_CONFIG_HOME/shadowtalk` or `~/.config/shadowtalk` |
| Windows | `%AppData%\shadowtalk` |

To use a different directory, put `--profile-dir` **before** the command:

```sh
shadowtalk --profile-dir ./data/demo ui
```

The private seed is encrypted in `identity-seed.json`; its encryption key stays in the OS credential store. ShadowTalk does not use a separate application password or fall back to storing that key in plaintext. Copying the profile directory alone is **not a portable backup** of the identity.

`profile.json` and `contacts.json` contain unencrypted public metadata, including contact relationships. `messages.db` encrypts message text with a profile-derived key; peers, IDs, directions, times, message lengths and delivery metadata remain visible. The chat command opens history and resumes the saved outgoing queue. A nickname or invitation signature does not replace fingerprint verification. Read the [security notes](docs/security.md) for the implemented protections and their limits.

## Roadmap

- [x] Local profiles and encrypted identity storage
- [x] Profile TUI
- [x] Separate secure-channel and Tor transport modules
- [x] Signed invitations and confirmed local contacts
- [x] Connect profiles, contacts, Tor and TLS in the peer-session core
- [x] Bounded text and acknowledgement wire protocol
- [x] Live one-to-one chat core and events for the interface
- [x] Connect peer sessions to CLI/TUI commands
- [x] Verify a complete exchange over the public Tor network (obfs4, macOS arm64)
- [ ] Verify chat between separate devices and complete platform smoke checks
- [x] Terminal chat screen
- [x] Encrypted local history storage API
- [x] Connect history to chat, acknowledgements and delivery retries
- [x] Release packaging and dependency notices for Windows, macOS and Linux
- [ ] Publish the first reviewed alpha release

Group rooms and a possible GUI are future directions; their scope is not committed. The terminal client comes first.

## Project structure

One Go module, one application binary:

```text
cmd/shadowtalk/          CLI commands and application entry point
internal/
  app/                  Profile, contact and network startup use cases
  identity/             Nicknames, identity keys and onion-key derivation
  contacts/             Signed invitations and contact rules
  chat/                 Live conversations, text exchange and application events
  history/              Persistent message records and paginated history API
  delivery/             Durable outgoing queue, ACK processing and retries
  peers/                Authenticated streams, connection limits and lifecycle
  protocol/             Message framing, IDs, versions and size limits
  onion/                Onion v3 address format
  securechannel/        TLS with mutual identity verification
  transport/tor/        Tor process, onion service and SOCKS connections
  storage/              Profile files, contacts, OS keys and encrypted SQLite history
  tui/                  Profile screens, chat, bounded history and safe rendering
docs/                   Architecture, protocol and design decisions
packaging/              Installers, license collection and release staging
tests/integration/      Tests across module and process boundaries
```

## Documentation

These links follow the checked-out branch or release tag. Most technical documents are in Russian; installation and release guides are in English.

| Document | Contents |
| --- | --- |
| [Project overview](docs/overview.md) | Goals and user scenarios |
| [Architecture](docs/architecture.md) | Module boundaries and dependencies |
| [Protocol](docs/protocol.md) | Invitations, text frames and acknowledgement format |
| [Security](docs/security.md) | Key storage, trust and limitations |
| [Tor transport](docs/tor.md) | Runtime setup, lifecycle and validation status |
| [Design decisions](docs/decisions) | Rationale behind the implementation |
| [Installation](docs/install.md) | Packages, runtime dependencies and first launch |
| [Releases](docs/releases.md) | Build process, dependency notices and publication |

<a id="разработка"></a>

## Development

Building from source requires **Go 1.26 or newer** and Git. Clone the repository:

```sh
git clone https://github.com/bacrehclb/ShadowTalk.git
cd ShadowTalk
```

For an existing release, check out its tag. To work on the next version, run
`git switch dev`, then create your feature branch.

```sh
# macOS / Linux
go build -o ./bin/shadowtalk ./cmd/shadowtalk
./bin/shadowtalk --version
./bin/shadowtalk ui
```

```powershell
# Windows PowerShell
go build -o ./bin/shadowtalk.exe ./cmd/shadowtalk
.\bin\shadowtalk.exe --version
.\bin\shadowtalk.exe ui
```

Run checks from the repository root:

```sh
go test ./...
go vet ./...
go build ./...
```

On macOS and Linux, also run `go test -race ./...`. CI runs tests, vet and builds on Windows, macOS and Linux, with the race detector enabled on macOS and Linux. Automated interactive terminal tests cover macOS and Linux; Windows terminal interaction still needs manual verification.

Regular tests use temporary data and substitute key stores. They do not access your real OS secrets or the public Tor network.

<details>
<summary>Optional checks with a real credential store or Tor</summary>

These checks are opt-in. The credential-store test creates a temporary profile and removes its test key. Tor tests require a Tor executable; the live test also needs network access. The examples below use macOS/Linux shell syntax.

```sh
# Native OS credential store; a system permission dialog may appear.
SHADOWTALK_NATIVE_KEYRING_TEST=1 go test ./cmd/shadowtalk -run '^TestNativeKeyStoreProfileLifecycle$' -count=1 -v

# Real Tor controller and onion key, without joining the Tor network.
SHADOWTALK_TOR_BINARY=/absolute/path/to/tor go test ./internal/transport/tor -run '^TestNativeTorControl$' -count=1 -v

# Real Lyrebird handshake and Tor bridge configuration, with networking disabled.
SHADOWTALK_TOR_BINARY=/absolute/path/to/tor SHADOWTALK_LYREBIRD_BINARY=/absolute/path/to/lyrebird go test ./internal/transport/tor -run '^TestNativeObfs4Control$' -count=1 -v

# Live Tor: bidirectional delivery, durable ACKs and offline queue recovery after restart.
SHADOWTALK_TOR_BINARY=/absolute/path/to/tor SHADOWTALK_TOR_LIVE_TEST=1 go test ./tests/integration -run '^TestTorSecureChannelLive$' -count=1 -v -timeout=15m
```

In PowerShell, set the corresponding variables with `$env:NAME = 'value'` before running `go test`. For a live bridge check, also set `SHADOWTALK_TOR_CONFIG` to your connection file; `SHADOWTALK_TOR_BINARY` overrides its binary path. The live Tor check uses temporary profiles, synthetic keys and encrypted SQLite history. It stops the receiver, queues a message, restarts both nodes and checks automatic delivery without duplicate history. By default it allows 3 minutes per Tor startup (configurable in the file) and 12 minutes overall, and can time out when the network is unavailable. Regular CI runs the same scenario with local test processes; that does not verify public Tor connectivity. See [Tor validation notes](docs/tor.md#ошибки-и-проверки) for observed results.

</details>

## Contributing

Bug reports, platform testing and focused improvements are welcome. For larger changes, open an [issue](https://github.com/bacrehclb/ShadowTalk/issues) first so we can agree on scope.

1. Fork the repository and create a branch from `dev`.
2. Keep changes focused; update relevant tests and documentation.
3. Run the checks above and open a pull request targeting `dev`.

For bug reports, include your OS, `shadowtalk --version` output and reproduction steps. For source builds, also include the commit and Go version. Keep private keys, real invitation tokens, bridge addresses, profile files and message contents out of issues and commits.

## License

[MIT](LICENSE) · Copyright © 2026 1tuz.

Distributed builds include `THIRD_PARTY_NOTICES.txt` with dependency and Go
toolchain license texts. See [license locations](docs/install.md#licenses).

---

<div align="center">
  <a href="README.md">English</a> · <a href="README.ru.md">Русский</a>
</div>
