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
    <a href="#troubleshooting">Troubleshooting</a> ·
    <a href="#roadmap">Roadmap</a> ·
    <a href="#documentation">Documentation</a>
  </p>

</div>

---

## Overview

ShadowTalk is an open-source terminal messenger written in Go. Create a local identity, exchange invitations with someone you trust, and communicate through Tor onion services without a shared ShadowTalk server. A nickname is a display name; your cryptographic key is your identity.

> **Experimental alpha:** one-to-one terminal chat, encrypted message history and delivery retries are implemented. Commands and storage formats may change between alpha versions. There has been no independent security audit.
>
> Current development is on **[main](https://github.com/bacrehclb/ShadowTalk/tree/main)**. For a released version, use its tag, release notes and bundled documentation.

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

Both participants install ShadowTalk, create their own profile and configure Tor.
Then exchange invitations **in both directions** and open the chat. The walkthrough
below uses Alice and Bob; run only the commands for your side on your own computer.

### 1. Install ShadowTalk

Choose a published version from [GitHub Releases](https://github.com/bacrehclb/ShadowTalk/releases)
and follow the [installation guide](docs/install.md). If no release is available,
use the `shadowtalk-packages` artifact from a successful
[Packages run](https://github.com/bacrehclb/ShadowTalk/actions/workflows/packages.yml?query=branch%3Amain)
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

**macOS:** open the DMG, run `Install ShadowTalk.pkg`, finish the installer, then
open Terminal. ShadowTalk installs a terminal command; there is no desktop app to open.

**Windows:** run the setup EXE, keep the PATH option enabled, then open a **new**
PowerShell or Windows Terminal window after installation.

**Linux:** open a terminal in the download directory and run the command for your
distribution. Replace `VERSION` and `ARCH` with the exact downloaded filename:

```sh
# Debian / Ubuntu
sudo apt install ./shadowtalk_VERSION_linux_ARCH.deb

# Fedora / distributions using DNF
sudo dnf install ./shadowtalk_VERSION_linux_ARCH.rpm
```

Run `shadowtalk --version` to check the installation. See the
[installation guide](docs/install.md) for checksum verification and removal.

### Requirements

- **Tor** for chat; **Lyrebird** as well when using obfs4 bridges. Install these separately and follow the [connection guide](docs/tor.md).
- **An accessible OS credential store:** Keychain on macOS, Credential Manager on Windows, or Secret Service over a user D-Bus session on Linux. Linux needs a running secret service with an available login/default collection; D-Bus alone is not enough.
- **An interactive terminal:** at least 40 × 20 for the profile UI and 60 × 20 for chat.

### 2. Create your profile

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

For this walkthrough, Alice can use `alice_01` and Bob `bob_01`. Once the profile
is shown, press **Esc** to return to the terminal. `ui` manages your profile;
the conversation opens later with `chat`.

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

<a id="tor-setup"></a>

### 3. Configure Tor on each computer

Download the stable [Tor Expert Bundle](https://download.torproject.org/tor/)
for your OS and processor, extract it to a permanent folder, and keep its
supporting libraries in place. Locate `tor` and, for bridges, `lyrebird`
(`tor.exe` and `lyrebird.exe` on Windows). ShadowTalk starts and stops its own
Tor process; opening Tor Browser does not configure ShadowTalk.

Choose **direct Tor** if your network allows it, or **obfs4** if you need bridges.
Alice and Bob may use different connection modes and different bridges.

Create a plain-text **UTF-8 file without a BOM**, named `tor.json`, in your
[profile directory](#local-data-and-security). Use one of these configurations.
Replace example paths with the actual executable paths on your computer.

**Direct connection:**

```json
{
  "version": 1,
  "mode": "direct",
  "tor_binary": "/absolute/path/to/tor",
  "startup_timeout": "5m"
}
```

If `tor` is already on PATH, omit `tor_binary`. With no default `tor.json`,
ShadowTalk also uses direct Tor from PATH, with a 3-minute startup timeout.

**Connection through obfs4 bridges:** get fresh **obfs4** bridge lines from
[Tor Project's bridge service](https://bridges.torproject.org/) or the alternatives
in its [bridge guide](https://support.torproject.org/little-t-tor/circumvention/using-bridges/).
Replace the entire example bridge line below with a real one:

```json
{
  "version": 1,
  "mode": "obfs4",
  "tor_binary": "/absolute/path/to/tor",
  "lyrebird_binary": "/absolute/path/to/lyrebird",
  "startup_timeout": "5m",
  "bridges": [
    "obfs4 IP:PORT FINGERPRINT cert=CERTIFICATE iat-mode=0"
  ]
}
```

Each bridge is one complete quoted string in the array, with commas **between**
strings. Keep its fingerprint, `cert` and `iat-mode` exactly as supplied. The
example is a placeholder, not a working bridge. ShadowTalk supports 1–16 obfs4
lines; Snowflake and WebTunnel are not supported by this version.

On **Windows**, use paths such as `C:/Tools/Tor/tor/tor.exe` and the actual path
to `lyrebird.exe`. Forward slashes work in JSON; backslashes must be doubled.
Make sure your editor saved `tor.json`, not `tor.json.txt`.

On **macOS/Linux**, restrict access after saving. Run only the command for your OS:

```sh
# macOS
chmod 600 "$HOME/Library/Application Support/shadowtalk/tor.json"

# Linux
chmod 600 "${XDG_CONFIG_HOME:-$HOME/.config}/shadowtalk/tor.json"
```

For a custom profile directory, use that directory instead. Keep bridge files
private. JSON does not allow comments or trailing commas; unknown fields are
rejected. Relative executable paths in JSON are resolved from the JSON file's
directory. See [Tor configuration](docs/tor.md#конфигурация-подключения) for details.

### 4. Exchange and confirm invitations

An invitation contains your public key, onion address and expiry time. It lets
the other person add you as a contact. **Both participants must add each other.**
Creating and inspecting invitations works without a running Tor connection.

**Alice creates her invitation** and displays her own fingerprint:

```sh
shadowtalk profile show
shadowtalk invite create --ttl 24h > alice.invite
```

**Bob creates his invitation** on his computer:

```sh
shadowtalk profile show
shadowtalk invite create --ttl 24h > bob.invite
```

In **Windows PowerShell**, use the matching command below instead of `>` so
the file is saved without an encoding marker. Run only your own line:

```powershell
# Alice
shadowtalk invite create --ttl 24h | Set-Content -Encoding ascii alice.invite
# Bob
shadowtalk invite create --ttl 24h | Set-Content -Encoding ascii bob.invite
```

Send `alice.invite` to Bob and `bob.invite` to Alice through a channel you already
use. The files are created in your terminal's current directory. Save the received
file there too, or pass its full path to `--file`. A token sent as text must remain
one intact line, without quotes, formatting or extra spaces.

**Alice inspects Bob's file:**

```sh
shadowtalk invite inspect --file bob.invite
```

Compare the **full fingerprint** with the one Bob sees in `shadowtalk profile show`,
using a trusted channel, for example in person or on a known voice call. A valid
signature confirms the key, not the person's identity. If they match, Alice adds
Bob; replace `SHA256:...` with Bob's complete confirmed fingerprint:

```sh
shadowtalk contacts add --alias bob --fingerprint "SHA256:..." --file bob.invite
shadowtalk contacts show --alias bob
```

**Bob does the same for Alice:** inspect her invitation, independently compare
the fingerprint with Alice, then use **Alice's** full fingerprint in the add command:

```sh
shadowtalk invite inspect --file alice.invite
shadowtalk contacts add --alias alice --fingerprint "SHA256:..." --file alice.invite
shadowtalk contacts show --alias alice
```

`--alias` is your local name for the contact: Alice uses `bob`, Bob uses `alice`.
It follows the nickname rules and need not match the other person's profile nick.
These commands save local contacts; the network starts when you run `chat`.

- Invitations expire after **24 hours** by default; `--ttl` accepts whole seconds from `1s` to `168h`.
- Tokens contain public identity data, including the onion address, but no private keys. Share them deliberately.
- Tokens can be reused until they expire. Single-use acceptance and revocation are not implemented. Saved contacts remain after token expiry.
- `--file -` reads one token from stdin until EOF. Results go to stdout, errors to stderr; failures return exit code `1`.

Remove a local contact by confirming its stored fingerprint:

```sh
shadowtalk contacts remove --alias alice --fingerprint SHA256:...
```

This removes the local entry; it does not revoke invitations. For command details, use `--help`, `invite --help` or `contacts --help`.

### 5. Send your first message

With the confirmed contacts saved and `tor.json` configured, **Alice runs**:

```sh
shadowtalk chat --contact bob
```

**Bob runs** on his computer:

```sh
shadowtalk chat --contact alice
```

Keep both chat windows open. Wait for each to finish connecting to Tor and
publishing its onion service; 100% bootstrap alone is not the end of startup.
Once the conversation appears, press **Tab** if needed to focus the message
editor, type a short message and press **Enter**. Ask the other person to reply.

The outgoing status changes from `ожидает доставки` (pending) to `доставлено`
(delivered) after the other device confirms saving it. `Tor готов` means your
own Tor node is ready; it does not prove that the other person is online.

For a configuration file outside the default profile directory:

```sh
shadowtalk chat --contact bob --tor-config "/absolute/path/to/tor.json"
```

If Tor is not on PATH, specify its executable (including `tor.exe` on Windows):

```sh
shadowtalk chat --tor-binary "/path/to/tor" --startup-timeout 5m
```

The screen shows Tor's bootstrap percentage and stage, then contacts, history and the message editor. The default Tor startup timeout is 3 minutes; `--startup-timeout` accepts 1 second to 15 minutes. History opens after Tor is ready. Omitting `--contact` selects the first saved contact. Contacts and invitations are managed using the commands above; restart chat to apply changes.

Connection settings can be saved in `tor.json` inside the profile directory, or selected with `chat --tor-config "/path/to/tor.json"`. Explicit `--tor-binary` and `--startup-timeout` flags override the file. A missing default file uses direct Tor; an invalid file stops startup. Retrying with Enter / `r` reloads the file.

Bridge mode never silently falls back to direct Tor. If startup fails, correct
the configuration or replace unavailable bridges, then press **Enter** or **r**
on the error screen to retry. Changes during a running chat require a restart.

### 6. Everyday use and keyboard controls

Next time, run `shadowtalk chat --contact bob` (Alice) or
`shadowtalk chat --contact alice` (Bob). You do not need to create another profile
or exchange new invitations for already saved contacts. To choose from all saved
contacts, run `shadowtalk chat`; `shadowtalk contacts list` lists their aliases.

| Key | Chat action |
| --- | --- |
| Tab | Switch between contacts and message input |
| ↑ / ↓ in contacts | Select a conversation |
| Enter | Open the selected conversation or queue the message |
| Alt+Enter / Ctrl+J | Insert a newline |
| PgUp / PgDn | Scroll history; PgUp at the top loads older records |
| Ctrl+L | Return to the latest messages and statuses |
| Ctrl+R after a save error | Retry the same message without changing its ID |
| Esc / Ctrl+C | Stop networking and exit |

A `•` beside a contact marks changes during this session. It is not a read receipt. **Pending** messages stay in the encrypted local queue; **delivered** means the peer acknowledged saving them. Both users must be online for delivery. Saved messages resume automatically on the next `chat` launch; unsaved drafts are kept only in memory. The screen keeps up to 200 history records at a time; the database retains older records.

Message text is limited to 16384 UTF-8 bytes. Oversized pastes are rejected, and terminal control sequences are displayed safely. `chat` requires terminal input and output; it does not accept piped messages. Details: `chat --help`.

Exit with **Esc** or **Ctrl+C** and let ShadowTalk finish stopping Tor. Delivery
runs while `chat` is open; there is no background mailbox server. After a restart,
saved history and queued messages use the same profile. Times in chat are UTC.

## Troubleshooting

| Symptom | What to check |
| --- | --- |
| `shadowtalk` is not found | Open a new terminal after installation. On Windows, enable the installer's PATH option; for a portable build, use the executable's full path. |
| `ui` shows a profile but no conversation | Exit with Esc and run `shadowtalk chat`; invitations and contacts are managed with the commands in step 4. |
| Tor or Lyrebird is not found | Point `tor_binary` / `lyrebird_binary` to the executable, not its folder. On Windows include `.exe`; keep Expert Bundle libraries in place. |
| Tor startup times out | Check connectivity, update obfs4 bridges if needed, and retry. You can use `--startup-timeout 10m`; the allowed maximum is `15m`. |
| `Небезопасный tor.json` / invalid configuration | Use a regular UTF-8 file without a BOM, valid JSON and real paths/bridge lines. On macOS/Linux run `chmod 600` on that file. |
| Invitation is expired or damaged | Ask the sender to create a new token. Preserve the entire line; use the ASCII command above in PowerShell. Check the system date/time on both devices. |
| Fingerprints do not match | Stop the import and verify the full value with the contact. Do not substitute an unverified fingerprint just to make the command pass. |
| Contact is missing or the alias is rejected | Run `shadowtalk contacts list`, check which profile directory you are using, and restart chat after importing a contact. |
| Messages remain pending | Both users must have `chat` running and Tor ready, and both must have imported each other's current identity. Keep the windows open for automatic retries. |
| Credential store is unavailable | Unlock/allow access to the OS key store. On Linux, use a user session with a running Secret Service and an unlocked login/default collection; do not run chat with `sudo`. |
| Window is too small | Enlarge it to at least 60 columns × 20 rows. Run in an interactive terminal with stdin/stdout attached. |

For a bug report, include your OS, `shadowtalk --version`, the command used and
the error text. Remove invitation tokens, bridge lines and personal paths before
sharing logs. Keep private keys, profile files and message history out of issues.

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
`git switch main`. Contributors can create their own feature branch from there.

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

1. Fork the repository and create a branch from `main`.
2. Keep changes focused; update relevant tests and documentation.
3. Run the checks above and open a pull request targeting `main`.

For bug reports, include your OS, `shadowtalk --version` output and reproduction steps. For source builds, also include the commit and Go version. Keep private keys, real invitation tokens, bridge addresses, profile files and message contents out of issues and commits.

## License

[MIT](LICENSE) · Copyright © 2026 1tuz.

Distributed builds include `THIRD_PARTY_NOTICES.txt` with dependency and Go
toolchain license texts. See [license locations](docs/install.md#licenses).

---

<div align="center">
  <a href="README.md">English</a> · <a href="README.ru.md">Русский</a>
</div>
