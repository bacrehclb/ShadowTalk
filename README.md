<div align="center">

  <h1>ShadowTalk</h1>

  <p>
    <b>A peer-to-peer messenger for your terminal.</b><br>
    Built in Go. Designed for Tor. Identity through cryptographic keys.
  </p>

  <p>
    <a href="https://go.dev/dl/"><img src="https://img.shields.io/badge/Go-1.26%2B-00ADD8?style=flat-square&logo=go&logoColor=white" alt="Go 1.26 or newer"></a>
    <a href="https://github.com/bacrehclb/ShadowTalk/actions/workflows/test.yml?query=branch%3Adev"><img src="https://img.shields.io/github/actions/workflow/status/bacrehclb/ShadowTalk/test.yml?branch=dev&event=push&style=flat-square&label=tests%20%28dev%29" alt="Tests on dev"></a>
    <a href="LICENSE"><img src="https://img.shields.io/badge/License-MIT-green?style=flat-square" alt="MIT License"></a>
    <br>
    <img src="https://img.shields.io/badge/Platforms-Windows%20%7C%20macOS%20%7C%20Linux-64748b?style=flat-square" alt="Windows, macOS and Linux">
    <img src="https://img.shields.io/badge/Status-early%20development-d97706?style=flat-square" alt="Early development">
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

ShadowTalk is an open-source terminal messenger in development. The goal is simple: create a local identity, exchange invitations with someone you trust, and communicate through Tor onion services without a shared ShadowTalk server. A nickname is a display name; your cryptographic key is your identity.

> **Early development:** profiles, signed invitations and local contacts are available. The core supports live text exchange, encrypted history and persistent delivery retries. Terminal chat screens are still pending.
>
> The features and commands below describe the **[dev branch](https://github.com/bacrehclb/ShadowTalk/tree/dev)**. `main` currently contains an earlier code snapshot; use `dev` to try the current implementation.

## What works today

| Component | Current implementation in `dev` |
| --- | --- |
| Local identity | Ed25519 keys, a full SHA-256 fingerprint and a persistent profile |
| Key storage | AES-256-GCM encrypted seed; encryption key stored in the OS credential store |
| Terminal UI | Create, view, upgrade and recover a local profile |
| Invitations | Signed tokens with an onion address, public key and expiration time |
| Contacts | Local aliases, explicit fingerprint confirmation and persistent storage |
| Secure channel | TLS 1.3 with mutual verification of confirmed identity keys |
| Tor transport | Managed Tor process and stable v3 onion address |
| Peer sessions | Core API connecting profiles, contacts, Tor and TLS, with connection limits and cancellation |
| Message protocol | Versioned text and acknowledgement frames, random message IDs and strict size limits |
| Chat core | Live conversations, text sending/receiving, bounded events and connection lifecycle; no automatic ACKs or retries |
| History storage | SQLite with encrypted message text, duplicate detection and cursor pagination |
| Delivery core | Persistent outbox, ACK after saving, retries after restart, and pending/delivered states; not yet exposed in CLI/TUI |

The network core is not connected to CLI/TUI commands yet. Local integration tests cover saved profiles, invitations and framed messages through TLS and simulated Tor processes; a complete exchange over the public Tor network has **not yet been verified**. The core uses a contact snapshot until restart. See the [Tor validation notes](https://github.com/bacrehclb/ShadowTalk/blob/dev/docs/tor.md).

## Quick start

### Requirements

- **Go 1.26 or newer** and Git to run from source.
- **An accessible OS credential store:** Keychain on macOS, Credential Manager on Windows, or Secret Service over a user D-Bus session on Linux. Linux needs a running secret service with an available login/default collection; D-Bus alone is not enough.
- **An interactive terminal** for the TUI, at least 40 × 20 characters.

Tor is not needed for the profile and contact commands below. Testing the Tor transport requires a separate Tor executable; see the [transport guide](https://github.com/bacrehclb/ShadowTalk/blob/dev/docs/tor.md).

```sh
git clone --branch dev https://github.com/bacrehclb/ShadowTalk.git
cd ShadowTalk
go run ./cmd/shadowtalk ui
```

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
go run ./cmd/shadowtalk profile create --nick alice_01
```

View your profile and public-key fingerprint:

```sh
go run ./cmd/shadowtalk profile show
```

Creating a profile does not overwrite an existing one. `profile upgrade` adds keys to an older profile that only contains metadata. `profile recover` handles interrupted creation; it cannot restore a lost OS encryption key.

### Exchange invitations

Create an invitation and send the resulting file to your intended contact:

```sh
go run ./cmd/shadowtalk invite create --ttl 24h > alice.invite
```

In Windows PowerShell, write the token as ASCII to avoid an encoding marker:

```powershell
go run ./cmd/shadowtalk invite create --ttl 24h | Set-Content -Encoding ascii alice.invite
```

The recipient inspects the invitation:

```sh
go run ./cmd/shadowtalk invite inspect --file alice.invite
```

**Compare the full fingerprint with the sender through a trusted channel.** A valid signature proves ownership of a key, not the identity of the person sending you the file. After checking, replace `SHA256:...` below with the full confirmed fingerprint:

```sh
go run ./cmd/shadowtalk contacts add --alias alice --fingerprint SHA256:... --file alice.invite
go run ./cmd/shadowtalk contacts list
go run ./cmd/shadowtalk contacts show --alias alice
```

Each participant needs their own profile. Exchange invitations in both directions to prepare mutual trust. These commands only save local contacts; they do not contact the other person or open a chat.

- Invitations expire after **24 hours** by default; `--ttl` accepts whole seconds from `1s` to `168h`.
- Tokens contain public identity data, including the onion address, but no private keys. Share them deliberately.
- Tokens can be reused until they expire. Single-use acceptance and revocation are not implemented. Saved contacts remain after token expiry.
- `--file -` reads one token from stdin until EOF. Results go to stdout, errors to stderr; failures return exit code `1`.

Remove a local contact by confirming its stored fingerprint:

```sh
go run ./cmd/shadowtalk contacts remove --alias alice --fingerprint SHA256:...
```

This removes the local entry; it does not revoke invitations. For command details, use `--help`, `invite --help` or `contacts --help`.

## Local data and security

| OS | Default profile directory |
| --- | --- |
| macOS | `~/Library/Application Support/shadowtalk` |
| Linux | `$XDG_CONFIG_HOME/shadowtalk` or `~/.config/shadowtalk` |
| Windows | `%AppData%\shadowtalk` |

To use a different directory, put `--profile-dir` **before** the command:

```sh
go run ./cmd/shadowtalk --profile-dir ./data/demo ui
```

The private seed is encrypted in `identity-seed.json`; its encryption key stays in the OS credential store. ShadowTalk does not use a separate application password or fall back to storing that key in plaintext. Copying the profile directory alone is **not a portable backup** of the identity.

`profile.json` and `contacts.json` contain unencrypted public metadata, including contact relationships. When used by the core API, `messages.db` encrypts message text with a profile-derived key; peers, IDs, directions, times, message lengths and delivery metadata remain visible. The history store is not yet opened by CLI/TUI commands. A nickname or invitation signature does not replace fingerprint verification. This is an unfinished messenger, not a claim of security readiness; the [security notes](https://github.com/bacrehclb/ShadowTalk/blob/dev/docs/security.md) describe the mechanisms and their limits.

## Roadmap

- [x] Local profiles and encrypted identity storage
- [x] Profile TUI
- [x] Separate secure-channel and Tor transport modules
- [x] Signed invitations and confirmed local contacts
- [x] Connect profiles, contacts, Tor and TLS in the peer-session core
- [x] Bounded text and acknowledgement wire protocol
- [x] Live one-to-one chat core and events for the interface
- [ ] Connect peer sessions to CLI/TUI commands
- [ ] Verify a complete exchange over the public Tor network
- [ ] Terminal chat screen
- [x] Encrypted local history storage API
- [x] Connect history to chat, acknowledgements and delivery retries
- [ ] Release packaging for Windows, macOS and Linux

Group rooms and a possible GUI are future directions; their scope is not committed. The terminal client comes first.

## Project structure

One Go module, one application binary. The current implementation lives in `dev`:

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
  tui/                  Terminal profile screens
docs/                   Architecture, protocol and design decisions
tests/integration/      Tests across module and process boundaries
```

## Documentation

These links point to the current documentation in `dev`. Technical documents are currently written in Russian.

| Document | Contents |
| --- | --- |
| [Project overview](https://github.com/bacrehclb/ShadowTalk/blob/dev/docs/overview.md) | Goals and user scenarios |
| [Architecture](https://github.com/bacrehclb/ShadowTalk/blob/dev/docs/architecture.md) | Module boundaries and dependencies |
| [Protocol](https://github.com/bacrehclb/ShadowTalk/blob/dev/docs/protocol.md) | Invitations, text frames and acknowledgement format |
| [Security](https://github.com/bacrehclb/ShadowTalk/blob/dev/docs/security.md) | Key storage, trust and limitations |
| [Tor transport](https://github.com/bacrehclb/ShadowTalk/blob/dev/docs/tor.md) | Runtime setup, lifecycle and validation status |
| [Design decisions](https://github.com/bacrehclb/ShadowTalk/tree/dev/docs/decisions) | Rationale behind the implementation |

<a id="разработка"></a>

## Development

Run checks from the repository root on `dev`:

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

# Two chat cores exchanging messages over the live Tor network.
SHADOWTALK_TOR_BINARY=/absolute/path/to/tor SHADOWTALK_TOR_LIVE_TEST=1 go test ./tests/integration -run '^TestTorSecureChannelLive$' -count=1 -v -timeout=7m
```

In PowerShell, set the corresponding variables with `$env:NAME = 'value'` before running `go test`. The live Tor check can time out when the network is unavailable; it is not part of regular CI.

</details>

Build a local executable:

```sh
# macOS / Linux
go build -o ./bin/shadowtalk ./cmd/shadowtalk
./bin/shadowtalk ui
```

```powershell
# Windows PowerShell
go build -o ./bin/shadowtalk.exe ./cmd/shadowtalk
.\bin\shadowtalk.exe ui
```

## Contributing

Bug reports, platform testing and focused improvements are welcome. For larger changes, open an [issue](https://github.com/bacrehclb/ShadowTalk/issues) first so we can agree on scope.

1. Fork the repository and create a branch from `dev`.
2. Keep changes focused; update relevant tests and documentation.
3. Run the checks above and open a pull request targeting `dev`.

For bug reports, include your OS, Go version, commit and reproduction steps. Keep private keys, real invitation tokens and profile files out of issues and commits.

## License

[MIT](LICENSE) · Copyright © 2026 1tuz.

---

<div align="center">
  <a href="README.md">English</a> · <a href="README.ru.md">Русский</a>
</div>
