#!/usr/bin/env python3
"""Validate alpha release inputs and stage assets locally; never publish them."""

import argparse
import hashlib
import json
from pathlib import Path
import re
import shutil
import subprocess

import package

ROOT = Path(__file__).resolve().parent.parent
ALPHA_TAG = re.compile(r"v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)-alpha\.[1-9][0-9]*")


def version_for(tag, commit):
    if not ALPHA_TAG.fullmatch(tag):
        raise ValueError("expected an alpha tag such as v0.1.0-alpha.1")
    if not re.fullmatch(r"[0-9a-f]{40}", commit):
        raise ValueError("expected a full, lowercase Git commit SHA")
    return tag[1:]


def notes_for(root, tag):
    notes = (root / "docs/releases" / f"{tag}.md").read_text(encoding="utf-8")
    if not notes.strip():
        raise ValueError("release notes must not be empty")
    return notes


def git(root, *args):
    return subprocess.check_output(
        ["git", "-C", str(root), *args], text=True, encoding="utf-8",
        stderr=subprocess.PIPE, timeout=30).strip()


def check_source(root, tag, commit):
    version_for(tag, commit)
    if git(root, "rev-parse", "HEAD") != commit:
        raise ValueError("checkout does not match the requested commit")
    if git(root, "rev-parse", "--verify", f"refs/tags/{tag}^{{commit}}") != commit:
        raise ValueError("tag does not point to the requested commit")
    if git(root, "merge-base", commit, "refs/remotes/origin/main") != commit:
        raise ValueError("release commit must already be merged into origin/main")
    if git(root, "status", "--porcelain"):
        raise ValueError("release checkout must be clean")
    notes_for(root, tag)


def bundle_names(version):
    prefix = f"shadowtalk_{version}"
    names = {"metadata.json", "homebrew/Casks/shadowtalk.rb"}
    for arch in ("arm64", "amd64"):
        for extension in ("tar.gz", "pkg", "dmg"):
            names.add(f"{prefix}_darwin_{arch}.{extension}")
        for extension in ("tar.gz", "deb", "rpm"):
            names.add(f"{prefix}_linux_{arch}.{extension}")
    names.update({f"{prefix}_windows_amd64.zip", f"{prefix}_windows_amd64_setup.exe"})
    return names


def digest(path):
    with path.open("rb") as stream:
        return hashlib.file_digest(stream, "sha256").hexdigest()


def validate_bundle(bundle, tag, commit):
    version = version_for(tag, commit)
    expected = bundle_names(version)
    actual = set()
    for path in bundle.rglob("*"):
        if path.is_symlink():
            raise ValueError("release bundle must not contain symlinks")
        if path.is_file():
            actual.add(path.relative_to(bundle).as_posix())
    if actual != expected | {"checksums.txt"}:
        raise ValueError(f"unexpected release contents; missing={sorted(expected - actual)}, "
                         f"extra={sorted(actual - expected - {'checksums.txt'})}")
    metadata = json.loads((bundle / "metadata.json").read_text(encoding="utf-8"))
    if not isinstance(metadata, dict) or any(metadata.get(key) != value for key, value in (
        ("project_name", "shadowtalk"), ("tag", tag), ("version", version), ("commit", commit),
    )):
        raise ValueError("release metadata does not match the requested tag and commit")
    checksums = {}
    for line in (bundle / "checksums.txt").read_text(encoding="utf-8").splitlines():
        checksum, separator, name = line.partition("  ")
        if not separator or not re.fullmatch(r"[0-9a-f]{64}", checksum) or name not in expected or name in checksums:
            raise ValueError("invalid, unexpected or duplicate checksum entry")
        checksums[name] = checksum
    if set(checksums) != expected:
        raise ValueError("checksums must cover every release file")
    for name, checksum in checksums.items():
        if (bundle / name).stat().st_size == 0 or digest(bundle / name) != checksum:
            raise ValueError(f"empty or corrupted release file: {name}")
    return expected


def prepare(root, bundle, output, tag, commit):
    # Validate everything before creating output. A previous staging directory
    # is deliberately not reused: stale release files must never be uploaded.
    if output.exists():
        raise ValueError("output already exists; choose a new staging directory")
    names = validate_bundle(bundle, tag, commit)
    notes = notes_for(root, tag)
    assets = output / "assets"
    assets.mkdir(parents=True)
    for name in sorted(names):
        shutil.copy2(bundle / name, assets / Path(name).name)
    # Release assets have flat names, unlike the workflow artifact's cask path.
    package.checksums(assets)
    (output / "release-notes.md").write_text(
        notes.rstrip() + f"\n\nBuilt from `{tag}` at commit `{commit}`.\n", encoding="utf-8")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    commands = parser.add_subparsers(dest="command", required=True)
    for command in ("check-source", "prepare"):
        subparser = commands.add_parser(command)
        subparser.add_argument("--tag", required=True)
        subparser.add_argument("--commit", required=True)
        if command == "prepare":
            subparser.add_argument("--bundle", required=True, type=Path)
            subparser.add_argument("--output", required=True, type=Path)
    args = parser.parse_args()
    try:
        if args.command == "check-source":
            check_source(ROOT, args.tag, args.commit)
        else:
            prepare(ROOT, args.bundle, args.output, args.tag, args.commit)
    except (OSError, ValueError, subprocess.SubprocessError) as error:
        parser.exit(1, f"release: {error}\n")
    print(f"{args.command}: {args.tag} verified")


if __name__ == "__main__":
    main()
