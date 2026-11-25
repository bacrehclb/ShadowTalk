"""Checks for archive contents and the optional, external DMG Canvas integration."""

import json
from pathlib import Path
import subprocess
import sys
import tarfile
import tempfile
import unittest

import package


class ArchiveNoticeTests(unittest.TestCase):
    def test_archive_must_include_the_current_notice_report(self):
        with tempfile.TemporaryDirectory(prefix="shadowtalk-archive-test-") as temporary:
            root = Path(temporary)
            (root / "metadata.json").write_text(json.dumps(dict(version="0.1.0", commit="fixture")))
            (root / "THIRD_PARTY_NOTICES.txt").write_bytes(b"ShadowTalk third-party notices\ncurrent")
            contents = root / "contents"
            for name in ("LICENSE", "README.md", "README.ru.md", "shadowtalk",
                         "docs/install.md", "docs/tor.md", "docs/testing/tor-live-exchange.md"):
                path = contents / name
                path.parent.mkdir(parents=True, exist_ok=True)
                path.write_bytes(b"fixture")
            for report, error in ((None, "missing distribution files"),
                                  (b"outdated", "third-party notices differ")):
                with self.subTest(report=report):
                    if report is not None:
                        (contents / "THIRD_PARTY_NOTICES.txt").write_bytes(report)
                    archive = root / "shadowtalk_0.1.0_darwin_arm64.tar.gz"
                    with tarfile.open(archive, "w:gz") as stream:
                        for path in contents.rglob("*"):
                            if path.is_file():
                                stream.add(path, arcname=path.relative_to(contents).as_posix())
                    # Validation fails before trying to execute the fixture binary.
                    with self.assertRaisesRegex(ValueError, error):
                        package.verify(root)


class CanvasTests(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory(prefix="shadowtalk-canvas-test-")
        self.addCleanup(self.temporary.cleanup)
        self.root = Path(self.temporary.name)
        self.template = self.root / "макет with spaces.dmgcanvas"
        self.template.mkdir()
        self.document = self.template / "Disk Image"
        self.entries = [dict(filePath="/old/" + name, isAnAlias=False)
                        for name in ("Install ShadowTalk.pkg", "Quick Start.txt")]

    def write_template(self, entries=None, version=4):
        self.document.write_text(json.dumps({
            "version": version,
            "diskImage": {"windowContents": self.entries if entries is None else entries},
        }), encoding="utf-8")

    def test_rejects_stale_or_unreplaceable_payloads(self):
        bad_entries = [[], self.entries[:1], self.entries + self.entries[:1],
                       [dict(filePath="/old/Other.pkg"), self.entries[1]],
                       [dict(self.entries[0], isAnAlias=True), self.entries[1]],
                       [42, self.entries[1]]]
        for entries in bad_entries:
            with self.subTest(entries=entries):
                self.write_template(entries=entries)
                with self.assertRaises(ValueError):
                    package.canvas_template(self.template)
        self.write_template(version=3)
        with self.assertRaises(ValueError):
            package.canvas_template(self.template)
        self.document.write_text("invalid JSON", encoding="utf-8")
        with self.assertRaises(ValueError):
            package.canvas_template(self.template)

    def test_cli_fails_before_building_when_template_is_missing(self):
        output = self.root / "must not be created"
        result = subprocess.run([
            sys.executable, str(Path(package.__file__)), "macos", "--dist", str(output),
            "--dmg-template", str(self.root / "missing.dmgcanvas"),
        ], stdin=subprocess.DEVNULL, capture_output=True, text=True, timeout=10)
        self.assertEqual(result.returncode, 2)
        self.assertIn("DMG Canvas 4", result.stderr)
        self.assertNotIn("Traceback", result.stderr)
        self.assertFalse(output.exists())

    @unittest.skipIf(sys.platform == "win32", "fake executable uses a POSIX shebang")
    def test_each_build_uses_current_payload_without_changing_template(self):
        self.write_template()
        original = self.document.read_bytes()
        template = package.canvas_template(self.template)
        helper = self.root / "fake dmgcanvas"
        helper.write_text('''#!/usr/bin/env python3
import json, sys
from pathlib import Path
args = sys.argv[1:]
payloads = {}
for i, arg in enumerate(args):
    if arg == "-setFilePath":
        payloads[args[i+1]] = Path(args[i+2]).read_text()
assert "-skipSigningAndNotarization" in args
Path(args[1]).write_text(json.dumps(payloads))
''', encoding="utf-8")
        helper.chmod(0o755)
        for arch in ("arm64", "amd64"):
            image = self.root / f"новая сборка {arch}"
            image.mkdir()
            (image / "Install ShadowTalk.pkg").write_text(arch)
            (image / "Quick Start.txt").write_text("current instructions")
            output = self.root / f"{arch}.dmg"
            package.build_dmg(image, output, template, helper)
            self.assertEqual(json.loads(output.read_text()), {
                "Install ShadowTalk.pkg": arch, "Quick Start.txt": "current instructions",
            })
        self.assertEqual(self.document.read_bytes(), original)


if __name__ == "__main__":
    unittest.main()
