# Packaging and alpha releases

GoReleaser OSS 2.20.3 owns the build matrix, linker metadata, portable archives,
Linux packages and the generated Homebrew cask. `packaging/package.py` wraps
those same archives with native installers: Apple's `pkgbuild`/`productbuild`
and `hdiutil` on macOS, Inno Setup 6 on Windows. Python 3.13 is a build-time
dependency only; end users do not need it or Go.

## Structure

```text
.goreleaser.yaml                 binaries, archives, deb/rpm, cask, metadata
.github/workflows/packages.yml   snapshot builds and native package smoke checks
cmd/shadowtalk/version.go        --version and linker variables
packaging/package.py             native installer orchestration and checksums
packaging/macos/                 native Installer text and disk-image readme
packaging/windows/               Inno Setup definition and native smoke check
docs/install.md                 user installation and runtime requirements
```

Packaging stays outside the messaging core. It never creates a profile,
configures bridges, accesses credentials or starts the chat. `--version` writes
one plain line to stdout and succeeds without network or profile access:

```text
shadowtalk VERSION (commit COMMIT, built DATE, OS/ARCH)
```

Plain `go build` reports `dev`, `unknown` commit and build date. GoReleaser
injects all three values; the current development workflow adds a snapshot
suffix with the commit. Local uncommitted changes can be part of a snapshot,
so only a clean CI checkout identifies an exact committed build.

## Local build without publication

Install the [pinned OSS GoReleaser](https://goreleaser.com/getting-started/install/oss/).
From the repository root:

```sh
goreleaser check
goreleaser release --snapshot --clean --skip=publish
python3 packaging/package.py verify

# On macOS: build both Intel and Apple Silicon PKG/DMG files.
python3 packaging/package.py macos
```

On Windows, after the GoReleaser build:

```powershell
python packaging/package.py windows --iscc "C:\Program Files (x86)\Inno Setup 6\ISCC.exe"
```

These commands write only build artifacts under `dist/` and temporary staging
directories. They do not run the resulting installers. The macOS PKG receipt
uses the numeric SemVer core because Apple's package metadata has a different
version format; the filename and executable retain the full snapshot/alpha
version. Do not use receipt version alone to distinguish prereleases.

GoReleaser's initial checksum list covers its own output. After adding native
installers or signing files, collect the final files into a clean directory and
run `python3 packaging/package.py checksums --dist PATH` to create the final
recursive SHA-256 manifest. Do not distribute the earlier checksum list as if
it covered installers.

## Custom macOS disk image (optional)

The standard build above uses `hdiutil` and requires no DMG Canvas license.
For a custom background, volume icon and layout, keep the editable
[DMG Canvas 4](https://www.araelium.com/dmgcanvas) project **outside this
repository**. Back up that directory separately; it is not included in Git.
For example:

```text
../ShadowTalk-design/dmg/
  ShadowTalk.dmgcanvas/    editable project, including embedded artwork
  assets/                 original background, icon source and .icns
  input/                  preview-only PKG and Quick Start.txt
```

The project's content list must have exactly two regular files named
`Install ShadowTalk.pkg` and `Quick Start.txt`, without aliases or extra payloads.
Keep those names when editing the layout. Preview inputs may be old: the build
script replaces both paths with fresh staged files **for each architecture**,
without modifying the project. Artwork is embedded in the Canvas project;
it is not copied into the Go source or portable archives.

After the GoReleaser snapshot build, run:

```sh
python3 packaging/package.py macos \
  --dmg-template ../ShadowTalk-design/dmg/ShadowTalk.dmgcanvas
```

The helper defaults to
`/Applications/DMG Canvas.app/Contents/Resources/dmgcanvas`. Use
`--dmgcanvas "/path/to/DMG Canvas.app/Contents/Resources/dmgcanvas"` to select
another official installation. The script always skips Canvas signing and
notarization; signing belongs in the separate release step. It never installs
the resulting PKG. Trial builds retain DMG Canvas's watermark.

Open both resulting DMGs and check the background, volume icon, readable file
names and current package contents before sharing them. The `.icns` in the
external design directory is also available for a future application bundle;
the current product installs a terminal command, not a GUI `.app`.

CI continues to build the standard DMG without private design files or a paid
tool. To distribute the custom DMGs, assemble them with the other final release
files and recalculate checksums after all packaging/signing is complete.
The integration checks run with:

```sh
python3 -m unittest discover -s packaging -p 'test_*.py'
```

## CI artifacts

The **Packages** workflow runs for PRs targeting `dev`/`main`, pushes to those
branches and manual dispatch. It has read-only repository permissions and uses
snapshot mode. There is no tag-triggered publisher or automatic tap push.

1. Ubuntu builds five binaries and archives with GoReleaser and four Linux
   packages. It installs/removes the amd64 DEB and runs the installed command
   outside the checkout.
2. macOS creates both architecture installers, installs the runner's native
   package, then checks its `--version`, `--help` and receipt.
3. Windows compiles Inno Setup, installs into a path containing spaces and
   Cyrillic, reinstalls without duplicating PATH, runs the command, then
   uninstalls while checking preservation of unrelated PATH entries and profile
   data. These mutations occur only on the ephemeral CI runner.
4. The final `shadowtalk-packages` artifact combines the archives, installers,
   metadata and local cask recipe, and recalculates checksums. It is retained
   for 14 days. Intermediate artifacts are retained for 7 days.

Artifacts require a successful run; adding a workflow is not evidence that it
passed. The ordinary **Tests** workflow remains required for code validation.
ARM64 Linux and the non-native macOS architecture are cross-built. RPM native
installation and interactive Windows installer appearance need separate checks.
Neither workflow establishes live messaging between separate computers.

## Publishing the first alpha

The intended first tag is `v0.1.0-alpha.1`; **do not tag or publish this snapshot
step**. Before publication:

- Review both workflow results and try the installation/user flow. Record the
  postponed two-device test and other unverified platforms as alpha limitations.
- Decide and configure publisher signing: Developer ID application/installer
  certificates and Apple notarization for macOS, Authenticode for Windows.
  The current pipeline builds unsigned packages and does not bypass OS checks.
- Review third-party notices before distribution; Tor/Lyrebird are currently
  separate dependencies and are not included in these packages.
- Add a release job that assembles the **tagged** artifacts, signs/notarizes
  them as applicable, computes final checksums and creates a reviewable GitHub
  prerelease. Keep the actual publication under the maintainer's control.
- Prepare a public `bacrehclb/homebrew-tap` repository if Homebrew distribution
  is wanted. The recipe is currently generated with `skip_upload: true`; the
  repo has not been created by this work. Publish the matching signed release
  assets before the cask. Do not publish a snapshot cask with nonexistent URLs.

After that setup, the intended macOS command is
`brew install --cask bacrehclb/tap/shadowtalk`. It is not usable yet. The PKG can
already be installed through Apple's `installer` command after downloading a
build artifact. Linux DEB/RPM files install locally through `apt`/`dnf` without
operating an APT/YUM repository. See [installation instructions](install.md).

## Tool references

- [GoReleaser archives](https://goreleaser.com/customization/package/archives/),
  [nFPM](https://goreleaser.com/customization/package/nfpm/),
  [Homebrew casks](https://goreleaser.com/customization/publish/homebrew_casks/)
- [Inno Setup](https://jrsoftware.org/inno-setup),
  [Windows runner tools](https://github.com/actions/runner-images/blob/main/images/windows/Windows2025-Readme.md)
- [Apple notarization workflow](https://developer.apple.com/documentation/security/customizing-the-notarization-workflow)
