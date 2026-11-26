#!/usr/bin/env python3
"""Fetch pinned Tor Expert Bundles into build output; never install on the host."""

import argparse
import hashlib
import json
import os
from pathlib import Path, PurePosixPath
import shutil
import ssl
import subprocess
import sys
import tarfile
import tempfile
import time
import urllib.error
import urllib.request

ROOT = Path(__file__).resolve().parent.parent
LOCK = ROOT / "packaging/tor-bundle.json"
CACHE = ROOT / "bin/tor-downloads"
OUTPUT = ROOT / "bin/tor-runtime"
MAX_ARCHIVE = 128 * 1024 * 1024
MAX_EXPANDED = 512 * 1024 * 1024


def digest(path):
    with path.open("rb") as stream:
        return hashlib.file_digest(stream, "sha256").hexdigest()


def lockfile():
    return json.loads(LOCK.read_text(encoding="utf-8"))


def tls_context():
    context = ssl.create_default_context()
    paths = ssl.get_default_verify_paths()
    # python.org's macOS installation can have no CA files until its optional
    # certificate setup is run. Use the OS CA bundle, never unverified HTTPS.
    if (sys.platform == "darwin" and not paths.cafile and not paths.capath
            and not os.environ.get("SSL_CERT_FILE") and not os.environ.get("SSL_CERT_DIR")
            and Path("/etc/ssl/cert.pem").is_file()):
        context.load_verify_locations(cafile="/etc/ssl/cert.pem")
    return context


def fetch(target, cache=CACHE):
    lock = lockfile()
    entry = lock["targets"][target]
    cache.mkdir(parents=True, exist_ok=True)
    archive = cache / entry["archive"]
    if archive.exists():
        if archive.is_symlink() or not archive.is_file() or digest(archive) != entry["sha256"]:
            raise ValueError(f"Tor cache checksum mismatch; remove and download again: {archive}")
        return archive
    url = lock["base_url"] + entry["archive"]
    context = tls_context()
    print(f"Downloading Tor {lock['tor_version']} for {target}...", flush=True)
    for attempt in range(3):
        temporary = None
        try:
            with tempfile.NamedTemporaryFile(dir=cache, prefix=".download-", delete=False) as output:
                temporary = Path(output.name)
                started, count = time.monotonic(), 0
                with urllib.request.urlopen(url, timeout=30, context=context) as response:
                    if not response.url.startswith("https://"):
                        raise ValueError("Tor download redirected outside HTTPS")
                    while block := response.read(1024 * 1024):
                        count += len(block)
                        if count > MAX_ARCHIVE or time.monotonic() - started > 300:
                            raise ValueError("Tor download exceeded its size/time limit")
                        output.write(block)
            if digest(temporary) != entry["sha256"]:
                raise ValueError(f"Tor download checksum mismatch: {entry['archive']}")
            os.replace(temporary, archive)
            return archive
        except (urllib.error.URLError, TimeoutError):
            if attempt == 2:
                raise
            time.sleep(1)
        finally:
            if temporary is not None:
                temporary.unlink(missing_ok=True)
    raise AssertionError("unreachable")


def extract(archive, destination):
    """Keep upstream layout and license texts; omit Linux debugging symbols."""
    seen, total = set(), 0
    with tarfile.open(archive) as source:
        members = source.getmembers()
        if len(members) > 1024:
            raise ValueError("too many Tor archive entries")
        for member in members:
            path = PurePosixPath(member.name)
            if (path.is_absolute() or ".." in path.parts or "\\" in member.name
                    or not path.parts or path.parts[0] not in {"tor", "data", "docs", "debug"}
                    or not (member.isdir() or member.isfile()) or member.name.casefold() in seen):
                raise ValueError(f"unsafe Tor archive entry: {member.name!r}")
            seen.add(member.name.casefold())
            total += member.size
            if member.size < 0 or total > MAX_EXPANDED:
                raise ValueError("Tor archive exceeded its expanded size limit")
        for member in members:
            if PurePosixPath(member.name).parts[0] == "debug":
                continue
            source.extract(member, destination, filter="data")
            path = destination / member.name
            path.chmod(0o755 if member.isdir() or member.mode & 0o111 else 0o644)


def inventory(directory):
    result = {}
    for path in sorted(directory.rglob("*")):
        if path.is_symlink() or not (path.is_file() or path.is_dir()):
            raise ValueError("Tor payload must contain only regular files and directories")
        if path.is_file() and path != directory / "runtime.json":
            result[path.relative_to(directory).as_posix()] = digest(path)
    return result


def required_files(target):
    suffix = ".exe" if target.startswith("windows-") else ""
    return {"tor/tor" + suffix, "tor/pluggable_transports/lyrebird" + suffix,
            "docs/tor.txt", "docs/lyrebird.txt", "docs/libevent.txt", "docs/openssl.txt"}


def verify(directory, target):
    if directory.is_symlink() or (directory / "runtime.json").is_symlink():
        raise ValueError("Tor runtime metadata must not be a symlink")
    metadata = json.loads((directory / "runtime.json").read_text(encoding="utf-8"))
    lock = lockfile()
    entry = lock["targets"][target]
    expected = dict(target=target, version=lock["version"], tor_version=lock["tor_version"], **entry)
    if any(metadata.get(key) != value for key, value in expected.items()):
        raise ValueError(f"wrong Tor runtime metadata for {target}")
    actual = inventory(directory)
    if actual != metadata.get("files") or not required_files(target).issubset(actual):
        raise ValueError(f"incomplete or changed Tor runtime for {target}")
    return metadata


def sign_macos(directory, target):
    """Repair upstream linker signatures in staged code, preserving provenance."""
    if sys.platform != "darwin" or not target.startswith("darwin-"):
        raise ValueError("Mac runtime signing requires macOS and a darwin target")
    metadata = verify(directory, target)
    for name in metadata["files"]:
        path = directory / name
        with path.open("rb") as stream:
            magic = stream.read(4)
        if magic not in (b"\xcf\xfa\xed\xfe", b"\xfe\xed\xfa\xcf", b"\xca\xfe\xba\xbe"):
            continue
        subprocess.run(["/usr/bin/codesign", "--force", "--sign", "-", str(path)],
                       check=True, capture_output=True, timeout=30)
        subprocess.run(["/usr/bin/codesign", "--verify", "--strict", str(path)],
                       check=True, capture_output=True, timeout=30)
    metadata["files"] = inventory(directory)
    metadata["signing"] = "ad-hoc"
    (directory / "runtime.json").write_text(json.dumps(metadata, indent=2) + "\n", encoding="utf-8")
    verify(directory, target)


def prepare(target, output=OUTPUT, cache=CACHE):
    archive = fetch(target, cache)
    output.mkdir(parents=True, exist_ok=True)
    destination = output / target
    # Fully validate the replacement before touching previously built output.
    with tempfile.TemporaryDirectory(dir=output, prefix=".tor-stage-") as temporary:
        stage = Path(temporary) / target
        stage.mkdir()
        extract(archive, stage)
        lock = lockfile()
        metadata = dict(target=target, version=lock["version"], tor_version=lock["tor_version"],
                        **lock["targets"][target], files=inventory(stage))
        (stage / "runtime.json").write_text(json.dumps(metadata, indent=2) + "\n", encoding="utf-8")
        verify(stage, target)
        if sys.platform == "darwin" and target.startswith("darwin-"):
            sign_macos(stage, target)
        if destination.is_symlink() or (destination.exists() and not destination.is_dir()):
            raise ValueError("Tor output is not a regular build directory")
        if destination.exists():
            shutil.rmtree(destination)
        stage.rename(destination)
    print(f"Prepared Tor runtime: {target}", flush=True)
    return destination


def smoke(directory, target):
    """Native executable check without Tor network access, profile or keyring."""
    metadata = verify(directory, target)
    suffix = ".exe" if target.startswith("windows-") else ""
    env = dict(os.environ)
    if target.startswith("linux-"):
        env["LD_LIBRARY_PATH"] = str((directory / "tor").resolve())
    for relative, flag, expected in (
        ("tor/tor" + suffix, "--version", metadata["tor_version"]),
        ("tor/pluggable_transports/lyrebird" + suffix, "-version", "lyrebird"),
    ):
        result = subprocess.run([str((directory / relative).resolve()), flag], cwd=directory.parent,
                                stdin=subprocess.DEVNULL, capture_output=True, text=True, timeout=20, check=True, env=env)
        if expected.lower() not in (result.stdout + result.stderr).lower():
            raise ValueError(f"unexpected version from bundled {relative}")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--target", choices=tuple(lockfile()["targets"]), action="append",
                        help="repeat to select targets; default: all supported targets")
    parser.add_argument("--output", type=Path, default=OUTPUT)
    parser.add_argument("--cache", type=Path, default=CACHE)
    args = parser.parse_args()
    try:
        for target in args.target or lockfile()["targets"]:
            prepare(target, args.output.resolve(), args.cache.resolve())
    except (OSError, ValueError, KeyError, tarfile.TarError, subprocess.SubprocessError, urllib.error.URLError) as error:
        parser.exit(1, f"tor-bundle: {error}\n")


if __name__ == "__main__":
    main()
