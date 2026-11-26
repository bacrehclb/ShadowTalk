# Build ShadowTalk.app

`ShadowTalk.app` is a macOS launcher for the existing terminal client. Double-click
the app to open Terminal with a menu:

1. **Профиль** — create or open your profile using the existing TUI.
2. **Чат** — open chat with your saved contacts and Tor configuration.
3. **Команды** — a command prompt for invitations, contacts and all CLI commands.
0. **Выход** — finish the launcher session.

Esc / Ctrl+C in a TUI returns to the menu after the operation finishes. In the
command prompt, `exit` or Ctrl+D returns to the menu. The menu itself does not
open a profile, access Keychain or start Tor until you choose an action.

The app includes both Apple Silicon and Intel binaries. The launcher requires
macOS 12 or newer; this is a build minimum, not a claim of testing on every
macOS version. Builds from this checkout include Tor and Lyrebird for both CPUs;
wrapping the original alpha.1 payload retains its separate-dependency behavior. Existing profiles
and `tor.json` keep their normal location in
`~/Library/Application Support/shadowtalk`.

## Compile from the current checkout

For a release image, use the [verified alpha bundle](#wrap-the-existing-alpha-without-rebuilding-its-go-binaries)
instead: the source-build command below intentionally produces **dev**. Before
packaging a release DMG, check `shadowtalk --version` in the app's command prompt.

On macOS, install Go 1.26+, Python 3.13+ and Apple's Xcode Command Line Tools
(or Xcode). From the repository root:

```sh
python3 packaging/macos_app.py
```

The result is **`dist/macos-app/ShadowTalk.app`**. The script compiles the current
Go sources for arm64 and amd64, downloads and verifies the pinned Tor runtimes,
collects dependency notices, builds the native
launcher and includes `packaging/macos/ShadowTalk.icns`. That icon was exported
from the same artwork as `docs/assets/shadowtalk-logo.png`.

Local source builds report `dev`, the current commit (with `-dirty` when there
are uncommitted changes), and the build time. They are not tagged alpha releases.
Nothing is installed, uploaded or added to PATH. No CI workflow is invoked.
The script uses a temporary directory and refuses an existing output; to keep
an earlier build and make another one:

```sh
python3 packaging/macos_app.py --output dist/macos-app-next/ShadowTalk.app
```

Open the result in Finder or run:

```sh
open dist/macos-app/ShadowTalk.app
```

In the app's **Команды** prompt, `shadowtalk` refers to the bundled binary.
This function is local to that shell: it does not edit `.zshrc`, `.bashrc` or PATH.
The prompt starts in your home directory; invitation files are created in the
current working directory. For example:

```sh
shadowtalk --version
shadowtalk invite create --ttl 24h > alice.invite
shadowtalk contacts --help
shadowtalk chat
```

The profile and bilateral invitation steps are in the
[English README](../README.md#usage) and [Russian README](../README.ru.md#использование).
From an unrelated terminal, use the full binary path or a separately installed
CLI. For an app copied into Applications:

```sh
"/Applications/ShadowTalk.app/Contents/MacOS/shadowtalk" --version
```

## Wrap the existing alpha without rebuilding its Go binaries

For the alpha DMG, start with the complete `shadowtalk-packages` artifact from
the successful **Alpha release** run. It contains both macOS portable archives,
`metadata.json`, notices, the other installers and its original full checksum
manifest. The compact public release downloads are not sufficient input.

```sh
python3 packaging/macos_app.py --bundle dist/alpha-input --output dist/macos-app-alpha/ShadowTalk.app
```

The builder verifies the entire bundle using the existing release validator,
combines the two verified Go executables into a universal binary, and adds the
current launcher. CLI version and source commit stay those of the alpha payload.
`Contents/Resources/build.json` separately records payload metadata, the launcher
checkout commit and whether that checkout had uncommitted changes. The bundled
documentation/notices come from the payload; `App Guide.md` describes this wrapper.

## Assemble your own DMG in DMG Canvas

1. Add the generated **whole `ShadowTalk.app`** to a new Canvas project.
2. Add an alias to `/Applications`, then arrange the app, arrow and Applications
   icon on your background.
3. Build the DMG. Open it, drag the app into Applications and launch the copied
   app. Both CPU architectures are already inside this one app.
4. Check the profile/chat/command menu and `shadowtalk --version` in the command
   prompt before distributing the image.

The old automated `package.py macos --dmg-template ...` integration still expects
a **PKG-based** Canvas project. Use Canvas directly for this new app layout.
The build script does not change that project or upload release assets.
The final first-alpha layout uses one universal DMG instead of the automated
draft's two architecture-specific DMGs, for eight release assets in total.
An app-only DMG does not install a global `shadowtalk` command; its own prompt
provides the command without administrator access.

The builder applies **ad-hoc code signatures** to the universal binary, bundled Tor code and app
and verifies them locally. These are not Developer ID signatures or Apple
notarization; downloaded copies may still be blocked by Gatekeeper. Publisher
signing/notarization is a separate release step. Do not describe these packages
as Apple-verified. Recalculate final DMG checksums after packaging/signing; the
existing PKG-DMG checksum is not valid for your new image.

## Local checks

```sh
python3 -m unittest discover -s packaging -p 'test_macos_app.py'
codesign --verify --deep --strict dist/macos-app/ShadowTalk.app
lipo -archs dist/macos-app/ShadowTalk.app/Contents/MacOS/shadowtalk
```

The builder also checks both architectures and runs `--help` / `--version` on
the host architecture without creating a profile. Native Intel execution and
live Tor messaging are separate from verifying a universal file.
