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
.github/workflows/release.yml    alpha tag checks, tests, packaging and draft release
cmd/shadowtalk/version.go        --version and linker variables
packaging/package.py             native installer orchestration and checksums
packaging/notices.py             runtime dependency license collection
packaging/licenses/              reviewed supplemental upstream license texts
packaging/release.py             source/bundle validation and local release staging
packaging/macos/                 native Installer text and disk-image readme
packaging/macos_app.py           optional universal app that opens Terminal
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
cp bin/notices/THIRD_PARTY_NOTICES.txt dist/THIRD_PARTY_NOTICES.txt
python3 packaging/package.py verify

# On macOS: build both Intel and Apple Silicon PKG/DMG files.
python3 packaging/package.py macos
```

On Windows, after the GoReleaser build:

```powershell
Copy-Item bin/notices/THIRD_PARTY_NOTICES.txt dist/THIRD_PARTY_NOTICES.txt
python packaging/package.py windows --iscc "C:\Program Files (x86)\Inno Setup 6\ISCC.exe"
```

These commands write only build artifacts under `bin/`, `dist/` and temporary staging
directories. They do not run the resulting installers. The macOS PKG receipt
uses the numeric SemVer core because Apple's package metadata has a different
version format; the filename and executable retain the full snapshot/alpha
version. Do not use receipt version alone to distinguish prereleases.

GoReleaser's initial checksum list covers its own output. After adding native
installers or signing files, collect the final files into a clean directory and
run `python3 packaging/package.py checksums --dist PATH` to create the final
recursive SHA-256 manifest. Do not distribute the earlier checksum list as if
it covered installers.

## Third-party notices

GoReleaser's `before` hook generates `bin/notices/THIRD_PARTY_NOTICES.txt` from the
runtime package graph for all five release targets, with CGO disabled. It uses
`go list -mod=readonly -deps` without tests. The report includes module versions,
original license/patent texts, nested notices on imported package paths, the
build's Go toolchain notices, and the reviewed Unicode supplement in
[`packaging/licenses`](../packaging/licenses/README.md). It does not modify
`go.mod` or `go.sum`. Modules must be available in the Go module cache or
downloadable through the normal Go module mechanism.

The intermediate report lives outside `dist/` because GoReleaser checks that
directory is empty after running global before hooks. Archives, Linux packages
and GoReleaser's checksum read it from `bin/notices/`; the next CI step copies it
to `dist/` as a standalone asset. The local commands above perform the same copy.

The same report ships in every portable archive, native installation and as a
separate release asset. Upstream third-party notice documents are preserved
verbatim, including any text about components that our binary does not use.
Tor and Lyrebird are separate downloads and are not included in this inventory.
ShadowTalk's own MIT license remains in `LICENSE`.

Missing/empty license texts and replaced modules stop generation for review.
Archive verification compares the included report with the generated one;
native CI installation checks compare installed bytes as well. Release staging
requires the report and its checksum. Build outputs stay in ignored `bin/` and `dist/`.

When updating dependencies or Go, review the generated report and any inline
notices in newly imported/generated code. Filename discovery cannot establish
license coverage for arbitrary dependencies. Keep `TARGETS` in `package.py`
aligned with GoReleaser, and update collection if build tags or CGO settings
change. Changes to supplemental texts and exclusions require source review.

## Custom macOS disk image (optional)

For a drag-to-Applications image, build the separate
[Terminal launcher app](macos-app.md) and arrange it manually in DMG Canvas.
The automated Canvas integration described below continues to use PKG inputs.

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
branches and manual dispatch. These runs use snapshot mode. It is also reusable:
the **Alpha release** workflow calls it with `release: true` for an actual tagged
version. Packaging jobs have read-only repository permissions; only the final
draft creation job can write releases. There is no automatic tap push.

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

### Prepare the main branch

Development PRs target `dev`; the reviewed release candidate reaches `main`
through a separate PR. A merge into `main` runs tests and snapshot packaging,
but does not create a release. Only pushing an alpha tag starts the draft flow.

If `main` contains changes absent from `dev`, merge `origin/main` into a
preparation branch based on `dev` and resolve conflicts there. Preserve both
parents when committing that merge. Merge its PR back into `dev` using
**Create a merge commit**, so the shared history survives; squash/rebase would
discard the merge parent. Then open the `dev` → `main` PR.

Before merging the candidate, check that:

- **Tests** and **Packages** pass for the current PR revision.
- Both READMEs describe the current commands and link to documentation in the
  same branch/tag; the installation guide matches the produced assets.
- The release notes distinguish completed checks from deferred live-device
  testing, and describe the actual signing status.
- The dependency notice report is included in the packages and release bundle.

The intended first tag is `v0.1.0-alpha.1`. Merging this implementation does not
create a tag or a release. First review **Tests** and **Packages**, try the
installation/user flow and merge the reviewed `dev` into `main` through a PR.
Keep [the alpha notes](releases/v0.1.0-alpha.1.md) aligned with the actual evidence.
The separate-device Tor test is postponed, and the notes explicitly say so.

The current pipeline creates **unsigned** packages. Before public distribution,
choose whether to ship the experimental alpha with that limitation or configure
Developer ID signing/notarization and Windows Authenticode in a separate step.
Do not describe these builds as signed. Review the generated third-party notice
report for the selected release; Tor/Lyrebird remain separate dependencies.

### Create the draft after the main merge

Only when the selected `main` commit is ready for release:

```sh
git switch main
git pull --ff-only origin main
git tag -a v0.1.0-alpha.1 -m "ShadowTalk v0.1.0-alpha.1"
git push origin v0.1.0-alpha.1
```

Pushing that tag starts **Alpha release**:

1. Require a strict `vMAJOR.MINOR.PATCH-alpha.N` tag (`N >= 1`), a clean checkout
   at its exact commit, ancestry in `origin/main`, and notes at
   `docs/releases/TAG.md`. A tag on an unmerged feature branch is rejected.
2. Run the existing Go tests, vet and build on all three operating systems.
3. Reuse **Packages** without `--snapshot`. Native installer checks still run.
4. Verify the complete CI bundle's tag/commit metadata, exact file inventory
   and every SHA-256 checksum. Select the public installers and notices, then
   generate a manifest covering only those downloads.
5. Use `gh release create --verify-tag --draft --prerelease --latest=false` to
   create an **unpublished** release. Publication is the maintainer's final
   manual action in GitHub Releases.

The draft has **nine uploaded assets**: two macOS DMGs, one Windows setup EXE,
four Linux DEB/RPM packages, `THIRD_PARTY_NOTICES.txt` and `checksums.txt`.
Each DMG contains its PKG installer. Five portable archives, standalone PKGs,
`metadata.json` and the generated cask stay in the complete `shadowtalk-packages`
CI artifact for verification and local repackaging. That artifact retains its
own complete checksum manifest; the public manifest covers only public files.
Notes identify the source commit, so metadata need not be a user download.
Only artifacts from this workflow run are used. The two source archives
[automatically provided by GitHub](https://docs.github.com/en/repositories/releasing-projects-on-github/about-releases)
are separate from these nine uploaded assets and their checksum manifest.

There is deliberately no automatic overwrite of existing releases. A rerun
stops if that tag already has a draft or a published release. If an upload
failed halfway, inspect and remove only the incomplete **draft** through GitHub
before rerunning the failed job. Never delete/rewrite a published release to
retry a build; use a new version. Build failures before draft creation can be
retried with GitHub's **Re-run failed jobs**.

### Add the locally designed DMG before publication

The automated draft contains standard DMGs. On a Mac, use the **same tag's**
checkout and its successful **Alpha release** run ID, not a development snapshot.
Start with new, empty paths under ignored `dist/`:

```sh
git switch --detach v0.1.0-alpha.1
gh run download RUN_ID --name shadowtalk-packages --dir dist/alpha-input
python3 packaging/package.py macos --dist dist/alpha-input \
  --dmg-template ../ShadowTalk-design/dmg/ShadowTalk.dmgcanvas

# Update all four macOS files in the full LOCAL bundle, including standalone PKGs.
mv dist/alpha-input/installers/* dist/alpha-input/
rmdir dist/alpha-input/installers
python3 packaging/package.py checksums --dist dist/alpha-input

# Stage public downloads with the reviewed compact exporter on main.
git switch main
python3 packaging/release.py prepare --tag v0.1.0-alpha.1 \
  --commit "$(git rev-parse 'v0.1.0-alpha.1^{commit}')" \
  --bundle dist/alpha-input --output dist/alpha-custom
```

Use `--dmgcanvas PATH` if needed. Preview each DMG and verify that its embedded
PKG matches the accompanying architecture-specific PKG. Finish any signing and
notarization before recalculating sums; modifying a PKG requires repackaging its
DMG too. The helper does not sign files. Trial Canvas builds retain the watermark.

The original `v0.1.0-alpha.1` tag predates compact public selection. Merge that
exporter change into `main` before the final staging command above; switching
back selects downloads from the completed tag bundle without rebuilding its
binaries. Do not move the existing tag to include this packaging-policy change.

While the GitHub release is still a **draft**, replace its two DMG assets and
`checksums.txt` with the matching files in `dist/alpha-custom/assets/`. Other
assets must remain identical to the downloaded bundle. Download the draft's
final assets and verify the manifest before publishing. After signing, update
the unsigned-build wording in the draft notes to describe exactly what was signed.
Do not use this replacement procedure on a published release.

`prepare` is local-only: it neither uploads nor publishes. It validates the full
CI bundle before selecting the public files; damage to an unpublished archive
or cask still fails validation. It refuses snapshots, mixed versions/commits,
missing or unexpected files, broken checksums and reused output directories.
A checksum proves correspondence to the supplied manifest;
it does not establish who built or signed a file.

### Homebrew and Linux

The cask is generated with `skip_upload: true`. A public
`bacrehclb/homebrew-tap` repository is a later distribution step; it has not been
created here. Its recipe references portable archives, which the compact public
set does not upload. A usable public tap requires a separate decision about
hosting those archives. The intended
`brew install --cask bacrehclb/tap/shadowtalk` command is not usable yet.
Linux DEB/RPM packages install locally through `apt`/`dnf` without an APT/YUM
repository. See [installation instructions](install.md).

## Tool references

- [GoReleaser archives](https://goreleaser.com/customization/package/archives/),
  [nFPM](https://goreleaser.com/customization/package/nfpm/),
  [Homebrew casks](https://goreleaser.com/customization/publish/homebrew_casks/)
- [Inno Setup](https://jrsoftware.org/inno-setup),
  [Windows runner tools](https://github.com/actions/runner-images/blob/main/images/windows/Windows2025-Readme.md)
- [Apple notarization workflow](https://developer.apple.com/documentation/security/customizing-the-notarization-workflow)
- [Reusable GitHub workflows](https://docs.github.com/en/actions/how-tos/reuse-automations/reuse-workflows),
  [GitHub CLI draft releases](https://cli.github.com/manual/gh_release_create),
  [GoReleaser build without publication](https://goreleaser.com/getting-started/quick-start/)
