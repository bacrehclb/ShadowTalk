#!/usr/bin/env python3
"""Wrap GoReleaser archives with native installers; never install on the host."""

import argparse
import hashlib
import json
import os
from pathlib import Path
import platform
import re
import shutil
import subprocess
import sys
import tarfile
import tempfile
import zipfile

ROOT = Path(__file__).resolve().parent.parent
DEFAULT_DMGCANVAS = Path("/Applications/DMG Canvas.app/Contents/Resources/dmgcanvas")
NOTICES = "THIRD_PARTY_NOTICES.txt"
TARGETS = (("darwin", "arm64"), ("darwin", "amd64"), ("linux", "arm64"),
           ("linux", "amd64"), ("windows", "amd64"))


def run(*args):
    subprocess.run([str(arg) for arg in args], check=True, timeout=300)


def unpack(archive, destination):
    if archive.suffix == ".zip":
        with zipfile.ZipFile(archive) as bundle:
            for name in bundle.namelist():
                if not (destination / name).resolve().is_relative_to(destination.resolve()):
                    raise ValueError("archive entry escapes destination")
            bundle.extractall(destination)
    else:
        with tarfile.open(archive) as bundle:
            bundle.extractall(destination, filter="data")


def canvas_template(path):
    """Validate the two replaceable payloads before invoking any build tools."""
    path = path.expanduser().resolve()
    try:
        document = json.loads((path / "Disk Image").read_text(encoding="utf-8"))
        entries = document["diskImage"]["windowContents"]
        names = [Path(entry["filePath"]).name for entry in entries if not entry.get("isAnAlias")]
    except (OSError, ValueError, KeyError, TypeError, AttributeError) as error:
        raise ValueError("expected a DMG Canvas 4 project containing a readable Disk Image document") from error
    if document.get("version") != 4 or len(entries) != 2 or sorted(names) != ["Install ShadowTalk.pkg", "Quick Start.txt"]:
        raise ValueError("Canvas project must contain exactly Install ShadowTalk.pkg and Quick Start.txt (not aliases)")
    return path


def build_dmg(image, output, template, dmgcanvas):
    if template is None:
        run("hdiutil", "create", "-volname", "ShadowTalk", "-srcfolder", image,
            "-format", "UDZO", "-ov", output)
        return
    # Replace preview inputs for EACH architecture. The external project stays
    # untouched, and a locally saved signing configuration cannot upload files.
    run(dmgcanvas, template, output, "-volumeName", "ShadowTalk",
        "-setFilePath", "Install ShadowTalk.pkg", image / "Install ShadowTalk.pkg",
        "-setFilePath", "Quick Start.txt", image / "Quick Start.txt",
        "-skipSigningAndNotarization")


def macos(dist, version, template=None, dmgcanvas=DEFAULT_DMGCANVAS):
    if sys.platform != "darwin":
        raise RuntimeError("macOS installers require a macOS host")
    output = dist / "installers"
    output.mkdir(exist_ok=True)
    for arch, host_arch in (("arm64", "arm64"), ("amd64", "x86_64")):
        name = f"shadowtalk_{version}_darwin_{arch}"
        with tempfile.TemporaryDirectory(prefix="shadowtalk-package-") as temporary:
            work = Path(temporary)
            unpack(dist / f"{name}.tar.gz", work / "archive")
            payload = work / "payload"
            binary = payload / "usr/local/bin/shadowtalk"
            binary.parent.mkdir(parents=True)
            shutil.copy2(work / "archive/shadowtalk", binary)
            binary.chmod(0o755)
            docs = payload / "usr/local/share/doc/shadowtalk"
            docs.mkdir(parents=True)
            for filename in ("LICENSE", NOTICES, "README.md", "README.ru.md"):
                shutil.copy2(work / "archive" / filename, docs / Path(filename).name)
            shutil.copytree(work / "archive/docs", docs / "docs")
            component = work / "component.pkg"
            # Apple's package receipt needs a numeric version. --version and
            # the downloaded filename retain the full prerelease identifier.
            numeric_version = re.match(r"\d+\.\d+\.\d+", version).group()
            run("pkgbuild", "--root", payload, "--identifier", "io.github.bacrehclb.shadowtalk",
                "--version", numeric_version, "--install-location", "/", component)
            # Productbuild copies this directory. Keep app-launcher source and
            # artwork out of the PKG's installer resources.
            resources = work / "installer-resources"
            resources.mkdir()
            for filename in ("welcome.html", "conclusion.html"):
                shutil.copy2(ROOT / "packaging/macos" / filename, resources / filename)
            distribution = work / "distribution.xml"
            distribution.write_text(f'''<?xml version="1.0" encoding="utf-8"?>
<installer-gui-script minSpecVersion="2">
  <title>ShadowTalk</title>
  <welcome file="welcome.html"/>
  <conclusion file="conclusion.html"/>
  <options customize="never" require-scripts="false" hostArchitectures="{host_arch}"/>
  <domains enable_anywhere="false" enable_currentUserHome="false" enable_localSystem="true"/>
  <choices-outline><line choice="default"/></choices-outline>
  <choice id="default" visible="false"><pkg-ref id="io.github.bacrehclb.shadowtalk"/></choice>
  <pkg-ref id="io.github.bacrehclb.shadowtalk" version="{numeric_version}">component.pkg</pkg-ref>
</installer-gui-script>
''', encoding="utf-8")
            pkg = output / f"{name}.pkg"
            run("productbuild", "--distribution", distribution, "--resources", resources,
                "--package-path", work, pkg)
            image = work / "image"
            image.mkdir()
            shutil.copy2(pkg, image / "Install ShadowTalk.pkg")
            shutil.copy2(ROOT / "packaging/macos/Read Me.txt", image / "Quick Start.txt")
            build_dmg(image, output / f"{name}.dmg", template, dmgcanvas)


def windows(dist, version, iscc):
    if sys.platform != "win32":
        raise RuntimeError("Windows installers require a Windows host with Inno Setup 6")
    output = dist / "installers"
    output.mkdir(exist_ok=True)
    with tempfile.TemporaryDirectory(prefix="shadowtalk-package-") as temporary:
        source = Path(temporary)
        unpack(dist / f"shadowtalk_{version}_windows_amd64.zip", source)
        run(iscc, f"/DAppVersion={version}", f"/DSourceDir={source}",
            f"/O{output}", ROOT / "packaging/windows/shadowtalk.iss")


def checksums(directory):
    # Recompute after ALL native packaging jobs. Never reuse a checksum for a
    # binary that was subsequently wrapped, signed, or otherwise changed.
    files = sorted(path for path in directory.rglob("*")
                   if path.is_file() and path.name != "checksums.txt")
    if not files:
        raise ValueError("no files to checksum")
    lines = []
    for path in files:
        with path.open("rb") as stream:
            digest = hashlib.file_digest(stream, "sha256").hexdigest()
        lines.append(f"{digest}  {path.relative_to(directory).as_posix()}\n")
    (directory / "checksums.txt").write_text("".join(lines), encoding="utf-8")


def verify(dist):
    """Exercise distributable files, including the executable for this host."""
    metadata = json.loads((dist / "metadata.json").read_text(encoding="utf-8"))
    version = metadata["version"]
    host_os = {"darwin": "darwin", "linux": "linux", "win32": "windows"}[sys.platform]
    host_arch = {"arm64": "arm64", "aarch64": "arm64", "amd64": "amd64", "x86_64": "amd64"}[platform.machine().lower()]
    notices = (dist / NOTICES).read_bytes()
    if not notices.startswith(b"ShadowTalk third-party notices\n"):
        raise ValueError("missing or invalid generated third-party notices")
    required = {"LICENSE", NOTICES, "README.md", "README.ru.md", "docs/install.md", "docs/tor.md",
                "docs/testing/tor-live-exchange.md"}
    for target_os, arch in TARGETS:
        suffix = "zip" if target_os == "windows" else "tar.gz"
        archive = dist / f"shadowtalk_{version}_{target_os}_{arch}.{suffix}"
        with tempfile.TemporaryDirectory(prefix="shadowtalk-verify-") as temporary:
            work = Path(temporary)
            unpack(archive, work / "unpacked")
            names = {p.relative_to(work / "unpacked").as_posix()
                     for p in (work / "unpacked").rglob("*") if p.is_file()}
            executable = "shadowtalk.exe" if target_os == "windows" else "shadowtalk"
            if not (required | {executable}).issubset(names):
                raise ValueError(f"missing distribution files in {archive.name}")
            if (work / "unpacked" / NOTICES).read_bytes() != notices:
                raise ValueError(f"third-party notices differ in {archive.name}")
            if any(name not in {executable, "LICENSE", NOTICES, "README.md", "README.ru.md"}
                   and not (name.startswith("docs/") and name.endswith(".md")) for name in names):
                raise ValueError(f"unexpected distribution files in {archive.name}")
            if (target_os, arch) == (host_os, host_arch):
                for flag in ("--version", "--help"):
                    result = subprocess.run(
                        [str(work / "unpacked" / executable), "--profile-dir", str(work / "profile"), flag],
                        cwd=work, stdin=subprocess.DEVNULL, capture_output=True, text=True,
                        encoding="utf-8", check=True, timeout=10)
                    if result.stderr or not result.stdout or (work / "profile").exists():
                        raise ValueError("help/version has side effects or invalid output")
                    if flag == "--version" and (version not in result.stdout or metadata["commit"] not in result.stdout):
                        raise ValueError("binary does not match release metadata")
            print(f"Verified {archive.name}")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("command", choices=("macos", "windows", "checksums", "verify"))
    parser.add_argument("--dist", type=Path, default=ROOT / "dist")
    parser.add_argument("--iscc", default="ISCC.exe")
    parser.add_argument("--dmg-template", type=Path, help="external DMG Canvas 4 project (macos only)")
    parser.add_argument("--dmgcanvas", type=Path, help="dmgcanvas executable; requires --dmg-template")
    args = parser.parse_args()
    template = None
    dmgcanvas = DEFAULT_DMGCANVAS
    if args.dmg_template is not None or args.dmgcanvas is not None:
        if args.command != "macos" or args.dmg_template is None:
            parser.error("--dmg-template and --dmgcanvas apply only to macos; --dmgcanvas requires --dmg-template")
        try:
            template = canvas_template(args.dmg_template)
        except ValueError as error:
            parser.error(str(error))
        dmgcanvas = (args.dmgcanvas or DEFAULT_DMGCANVAS).expanduser().resolve()
        if not dmgcanvas.is_file() or not os.access(dmgcanvas, os.X_OK):
            parser.error("dmgcanvas executable not found or not executable; use --dmgcanvas PATH")
    dist = args.dist.resolve()
    if args.command == "checksums":
        checksums(dist)
        return
    if args.command == "verify":
        verify(dist)
        return
    version = json.loads((dist / "metadata.json").read_text(encoding="utf-8"))["version"]
    if not re.fullmatch(r"\d+\.\d+\.\d+(?:[-+][a-zA-Z0-9.-]+)?", version):
        raise ValueError("unexpected GoReleaser version")
    if args.command == "macos":
        macos(dist, version, template, dmgcanvas)
    else:
        windows(dist, version, args.iscc)


if __name__ == "__main__":
    main()
