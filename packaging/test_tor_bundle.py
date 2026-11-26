import hashlib
import io
import json
from pathlib import Path
import tarfile
import tempfile
import unittest
from unittest.mock import patch

import tor_bundle


class BundleTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.cache = self.root / "cache"
        self.cache.mkdir()
        self.output = self.root / "output"
        self.archive = self.cache / "fixture.tar.gz"
        self.lock = dict(version="test", tor_version="1.2.3", base_url="https://example.invalid/",
                         targets={"linux-amd64": dict(archive=self.archive.name, sha256="")})
        self.patcher = patch.object(tor_bundle, "lockfile", return_value=self.lock)
        self.patcher.start()
        self.addCleanup(self.patcher.stop)

    def archive_with(self, extra=()):
        with tarfile.open(self.archive, "w:gz") as bundle:
            for name in sorted(tor_bundle.required_files("linux-amd64") | {"tor/libssl.so.3", "debug/tor"}):
                member = tarfile.TarInfo(name)
                member.mode = 0o755 if name.startswith("tor/") else 0o644
                member.size = len(b"fixture")
                bundle.addfile(member, io.BytesIO(b"fixture"))
            for member in extra:
                bundle.addfile(member, io.BytesIO(b""))
        self.lock["targets"]["linux-amd64"]["sha256"] = tor_bundle.digest(self.archive)

    def test_valid_bundle_preserves_libraries_licenses_and_detects_corruption(self):
        self.archive_with()
        target = tor_bundle.prepare("linux-amd64", self.output, self.cache)
        self.assertTrue((target / "tor/libssl.so.3").is_file())
        self.assertTrue((target / "docs/lyrebird.txt").is_file())
        self.assertFalse((target / "debug").exists())
        self.assertEqual(tor_bundle.verify(target, "linux-amd64")["version"], "test")
        (target / "tor/tor").write_bytes(b"damaged")
        with self.assertRaisesRegex(ValueError, "changed"):
            tor_bundle.verify(target, "linux-amd64")

    def test_bad_cached_download_does_not_replace_existing_payload(self):
        self.archive_with()
        destination = tor_bundle.prepare("linux-amd64", self.output, self.cache)
        before = (destination / "runtime.json").read_bytes()
        self.archive.write_bytes(b"interrupted download")
        with self.assertRaisesRegex(ValueError, "checksum mismatch"):
            tor_bundle.prepare("linux-amd64", self.output, self.cache)
        self.assertEqual((destination / "runtime.json").read_bytes(), before)

    def test_archive_paths_links_duplicates_and_devices_are_rejected(self):
        for kind in ("escape", "absolute", "link", "duplicate", "device"):
            with self.subTest(kind=kind):
                member = tarfile.TarInfo({"escape": "../escape", "absolute": "/tmp/escape",
                                          "duplicate": "tor/tor"}.get(kind, "tor/extra"))
                if kind == "link":
                    member.type, member.linkname = tarfile.SYMTYPE, "../../escape"
                if kind == "device":
                    member.type = tarfile.CHRTYPE
                self.archive_with([member])
                with self.assertRaisesRegex(ValueError, "unsafe"):
                    tor_bundle.prepare("linux-amd64", self.output, self.cache)
                self.assertFalse((self.output / "linux-amd64").exists())
                self.assertFalse((self.output / "escape").exists())

    def test_download_verified_before_publication_and_temp_cleaned_on_error(self):
        self.lock["targets"]["linux-amd64"]["sha256"] = hashlib.sha256(b"expected").hexdigest()
        for data, success in ((b"corrupt", False), (b"expected", True)):
            response = io.BytesIO(data)
            response.url = "https://example.invalid/fixture.tar.gz"
            with patch.object(tor_bundle.urllib.request, "urlopen", return_value=response):
                if success:
                    self.assertEqual(tor_bundle.fetch("linux-amd64", self.cache).read_bytes(), data)
                else:
                    with self.assertRaisesRegex(ValueError, "checksum mismatch"):
                        tor_bundle.fetch("linux-amd64", self.cache)
                    self.assertFalse(self.archive.exists())
                self.assertEqual(list(self.cache.glob(".download-*")), [])

    def test_missing_program_or_license_cannot_be_hidden_in_inventory(self):
        self.archive_with()
        target = tor_bundle.prepare("linux-amd64", self.output, self.cache)
        (target / "docs/tor.txt").unlink()
        metadata = json.loads((target / "runtime.json").read_text())
        metadata["files"] = tor_bundle.inventory(target)
        (target / "runtime.json").write_text(json.dumps(metadata))
        with self.assertRaisesRegex(ValueError, "incomplete"):
            tor_bundle.verify(target, "linux-amd64")


if __name__ == "__main__":
    unittest.main()
