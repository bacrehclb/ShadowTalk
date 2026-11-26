#!/usr/bin/env python3
"""Build a universal Terminal launcher app locally; never install or publish it."""

import argparse
from datetime import datetime, timezone
import json
import os
from pathlib import Path
import plistlib
import re
import shutil
import subprocess
import sys
import tempfile

import notices
import package
import release

ROOT = package.ROOT
RESOURCES = ROOT / "packaging/macos"
IDENTIFIER = "io.github.bacrehclb.shadowtalk.launcher"
LAUNCHER = "ShadowTalkLauncher"  # Distinct from shadowtalk on case-insensitive APFS.


def run(*args, **kwargs):
    subprocess.run([str(arg) for arg in args], check=True, timeout=300, **kwargs)


def source_payload(work):
    """A local build is always dev, even when the checkout has an alpha tag."""
    commit = release.git(ROOT, "rev-parse", "HEAD")
    if release.git(ROOT, "status", "--porcelain"):
        commit += "-dirty"
    date = datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")
    binaries = []
    for arch in ("arm64", "amd64"):
        print(f"Compiling Go payload for {arch}...", flush=True)
        binary = work / f"shadowtalk-{arch}"
        env = dict(os.environ, GOOS="darwin", GOARCH=arch, CGO_ENABLED="0")
        run("go", "build", "-mod=readonly", "-trimpath", "-ldflags",
            f"-s -w -X main.version=dev -X main.commit={commit} -X main.date={date}",
            "-o", binary, "./cmd/shadowtalk", cwd=ROOT, env=env)
        binaries.append(binary)
    report = work / package.NOTICES
    print("Collecting dependency notices...", flush=True)
    report.write_bytes(notices.generate(ROOT))
    return binaries, ROOT, report, {"version": "dev", "commit": commit, "date": date}


def release_payload(bundle, work):
    """Reuse the verified alpha binaries and their own license report."""
    metadata = json.loads((bundle / "metadata.json").read_text(encoding="utf-8"))
    release.validate_bundle(bundle, metadata["tag"], metadata["commit"])
    binaries = []
    for arch in ("arm64", "amd64"):
        directory = work / arch
        package.unpack(bundle / f"shadowtalk_{metadata['version']}_darwin_{arch}.tar.gz", directory)
        binary = directory / "shadowtalk"
        if not binary.is_file() or binary.is_symlink():
            raise ValueError("archive must contain a regular shadowtalk executable")
        if (directory / package.NOTICES).read_bytes() != (bundle / package.NOTICES).read_bytes():
            raise ValueError("archive notices differ from the verified release bundle")
        binaries.append(binary)
    return binaries, work / "arm64", bundle / package.NOTICES, metadata


def build(output, bundle=None):
    if sys.platform != "darwin":
        raise ValueError("building ShadowTalk.app requires macOS")
    if output.suffix != ".app":
        raise ValueError("--output must end in .app")
    if output.exists() or output.is_symlink():
        raise ValueError("output already exists; choose a new --output path")
    output.parent.mkdir(parents=True, exist_ok=True)
    # Stage beside the destination: a failed build never leaves a partial .app.
    with tempfile.TemporaryDirectory(prefix=".shadowtalk-app-", dir=output.parent) as temporary:
        work = Path(temporary)
        binaries, docs_source, report, metadata = (
            release_payload(bundle, work) if bundle else source_payload(work))
        app = work / "ShadowTalk.app"
        contents = app / "Contents"
        executables = contents / "MacOS"
        resources = contents / "Resources"
        executables.mkdir(parents=True)
        resources.mkdir()
        for binary, arch in zip(binaries, ("arm64", "x86_64")):
            run("/usr/bin/lipo", binary, "-verify_arch", arch)
        run("/usr/bin/lipo", "-create", *binaries, "-output", executables / "shadowtalk")
        (executables / "shadowtalk").chmod(0o755)
        run("/usr/bin/xcrun", "clang", "-fobjc-arc", "-O2", "-Wall", "-Wextra",
            "-Wno-unused-parameter", "-mmacosx-version-min=12.0",
            "-arch", "arm64", "-arch", "x86_64", "-framework", "AppKit",
            RESOURCES / "launcher.m", "-o", executables / LAUNCHER)
        for name in ("ShadowTalk.command", "terminal.bash", "ShadowTalk.icns"):
            shutil.copyfile(RESOURCES / name, resources / name)
        (resources / "ShadowTalk.command").chmod(0o755)
        docs = resources / "Documentation"
        docs.mkdir()
        for name in ("LICENSE", "README.md", "README.ru.md"):
            shutil.copyfile(docs_source / name, docs / name)
        shutil.copytree(docs_source / "docs", docs / "docs", ignore=shutil.ignore_patterns(".DS_Store"))
        shutil.copyfile(report, docs / package.NOTICES)
        # A small guide describes the new wrapper even when its payload is an old tag.
        shutil.copyfile(ROOT / "docs/macos-app.md", resources / "App Guide.md")
        version = metadata["version"]
        core = re.match(r"\d+\.\d+\.\d+", version)
        numeric = core.group() if core else "0.0.0"
        info = {
            "CFBundleIdentifier": IDENTIFIER,
            "CFBundleName": "ShadowTalk",
            "CFBundleDisplayName": "ShadowTalk",
            "CFBundleExecutable": LAUNCHER,
            "CFBundlePackageType": "APPL",
            "CFBundleIconFile": "ShadowTalk.icns",
            "CFBundleShortVersionString": numeric,
            "CFBundleVersion": numeric,
            "LSMinimumSystemVersion": "12.0",
            "LSUIElement": True,
            "NSHighResolutionCapable": True,
            "ShadowTalkVersion": version,
        }
        (contents / "Info.plist").write_bytes(plistlib.dumps(info))
        provenance = {
            "payload": metadata,
            "packaging_commit": release.git(ROOT, "rev-parse", "HEAD"),
            "packaging_dirty": bool(release.git(ROOT, "status", "--porcelain")),
            "architectures": ["arm64", "x86_64"],
            "signing": "ad-hoc; no Developer ID or notarization",
        }
        (resources / "build.json").write_text(json.dumps(provenance, indent=2) + "\n", encoding="utf-8")
        # Universal merging changes the Mach-O layout. Sign nested code first;
        # ad-hoc signatures permit local execution, not publisher verification.
        run("/usr/bin/codesign", "--force", "--sign", "-", executables / "shadowtalk")
        run("/usr/bin/codesign", "--force", "--sign", "-", app)
        run("/usr/bin/codesign", "--verify", "--deep", "--strict", app)
        for name in ("shadowtalk", LAUNCHER):
            for arch in ("arm64", "x86_64"):
                run("/usr/bin/lipo", executables / name, "-verify_arch", arch)
        for flag in ("--version", "--help"):
            result = subprocess.run([str(executables / "shadowtalk"), "--profile-dir",
                                     str(work / "profile"), flag], cwd=work,
                                    stdin=subprocess.DEVNULL, capture_output=True, text=True,
                                    encoding="utf-8", check=True, timeout=10)
            if result.stderr or not result.stdout or (work / "profile").exists():
                raise ValueError(f"bundled {flag} failed: stdout={bool(result.stdout)}, "
                                 f"stderr={result.stderr.strip()!r}, "
                                 f"profile_created={(work / 'profile').exists()}")
            if flag == "--version" and any(value not in result.stdout for value in (version, metadata["commit"])):
                raise ValueError("bundled executable does not match its source metadata")
        if output.exists() or output.is_symlink():
            raise ValueError("output appeared during build; choose a new --output path")
        app.rename(output)
    print(f"Built {output} (Apple Silicon + Intel)")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--output", type=Path, default=ROOT / "dist/macos-app/ShadowTalk.app")
    parser.add_argument("--bundle", type=Path, help="verified complete alpha CI bundle; otherwise compile current sources as dev")
    args = parser.parse_args()
    try:
        # Do not resolve the last component: an existing output symlink is refused.
        output = Path(os.path.abspath(args.output.expanduser()))
        bundle = args.bundle.expanduser().resolve() if args.bundle else None
        build(output, bundle)
    except (OSError, ValueError, KeyError, subprocess.SubprocessError) as error:
        parser.exit(1, f"macos-app: {error}\n")


if __name__ == "__main__":
    main()
