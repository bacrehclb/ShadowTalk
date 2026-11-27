import os
from pathlib import Path
import select
import shutil
import subprocess
import sys
import tempfile
import time
import unittest

import macos_app


class AppBuildTests(unittest.TestCase):
    def test_help_does_not_create_output(self):
        with tempfile.TemporaryDirectory() as directory:
            output = Path(directory) / "ShadowTalk.app"
            result = subprocess.run([sys.executable, str(Path(macos_app.__file__)),
                                     "--output", str(output), "--help"],
                                    capture_output=True, text=True, timeout=10)
            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertIn("--bundle", result.stdout)
            self.assertFalse(output.exists())

    @unittest.skipUnless(sys.platform == "darwin", "macOS builder")
    def test_existing_output_is_preserved(self):
        with tempfile.TemporaryDirectory() as directory:
            output = Path(directory) / "ShadowTalk.app"
            output.mkdir()
            marker = output / "keep.txt"
            marker.write_text("previous build")
            with self.assertRaisesRegex(ValueError, "already exists"):
                macos_app.build(output)
            self.assertEqual(marker.read_text(), "previous build")


@unittest.skipUnless(os.name == "posix" and Path("/bin/bash").exists(), "Bash with a PTY required")
class TerminalMenuTests(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory(prefix="shadowtalk-launcher-")
        self.addCleanup(self.temporary.cleanup)
        self.root = Path(self.temporary.name)
        # Relocated app, including characters that must never become shell code.
        self.app = self.root / "папка ' $(touch GOTCHA)" / "Moved App.app"
        self.resources = self.app / "Contents/Resources"
        self.resources.mkdir(parents=True)
        binary = self.app / "Contents/MacOS/shadowtalk"
        binary.parent.mkdir()
        binary.write_text('#!/bin/bash\nprintf "%s\\n" "$*" >> "$CALL_LOG"\n'
                          'if [[ "$1" == menu && "${LEGACY:-}" == 1 ]]; then exit 1; fi\n'
                          'if [[ "$1" == ui ]]; then exit 7; fi\n', encoding="utf-8")
        binary.chmod(0o755)
        for name in ("ShadowTalk.command", "terminal.bash"):
            shutil.copyfile(macos_app.RESOURCES / name, self.resources / name)
        self.env = dict(os.environ, HOME=str(self.root), TERM="xterm-256color",
                        CALL_LOG=str(self.root / "calls.txt"))
        self.command = ["/bin/bash", str(self.resources / "ShadowTalk.command")]

    def test_non_terminal_fails_without_touching_profile(self):
        result = subprocess.run(self.command, env=self.env, stdin=subprocess.DEVNULL,
                                capture_output=True, text=True, timeout=5)
        self.assertEqual(result.returncode, 1)
        self.assertIn("Terminal", result.stderr)
        self.assertFalse((self.root / "calls.txt").exists())

    def test_relocated_menu_and_command_prompt_use_bundled_binary(self):
        self.env["LEGACY"] = "1"
        import pty
        master, slave = pty.openpty()
        self.addCleanup(os.close, master)
        process = subprocess.Popen(self.command, env=self.env, cwd=self.root,
                                   stdin=slave, stdout=slave, stderr=slave)
        os.close(slave)
        def stop():
            if process.poll() is None:
                process.kill()
            process.wait(timeout=5)
        self.addCleanup(stop)
        output = bytearray()

        def expect(fragment):
            expected = fragment.encode()
            deadline = time.monotonic() + 8
            while expected not in output:
                self.assertLess(time.monotonic(), deadline, output.decode(errors="replace"))
                if select.select([master], [], [], 0.1)[0]:
                    try:
                        output.extend(os.read(master, 8192))
                    except OSError:
                        self.fail(output.decode(errors="replace"))
                self.assertLess(len(output), 65536)
            output.clear()

        def send(line):
            os.write(master, (line + "\n").encode())

        expect("Выберите действие")
        send("1")
        expect("кодом 7")
        send("2")
        expect("Выберите действие")
        send("3")
        expect("возврата в меню")
        send("shadowtalk invite inspect --file 'received token.invite'")
        send("exit")
        expect("Выберите действие")
        send("0")
        self.assertEqual(process.wait(timeout=5), 0)
        self.assertEqual((self.root / "calls.txt").read_text().splitlines(),
                         ["menu --help", "ui", "chat", "invite inspect --file received token.invite"])
        self.assertFalse((self.root / "GOTCHA").exists())
        self.assertFalse((self.root / "Library").exists())

    def test_current_payload_opens_go_menu(self):
        import pty
        master, slave = pty.openpty()
        try:
            result = subprocess.run(self.command, env=self.env, cwd=self.root,
                                    stdin=slave, stdout=slave, stderr=slave, timeout=5)
        finally:
            os.close(slave)
            os.close(master)
        self.assertEqual(result.returncode, 0)
        self.assertEqual((self.root / "calls.txt").read_text().splitlines(),
                         ["menu --help", "menu"])
        self.assertFalse((self.root / "GOTCHA").exists())


if __name__ == "__main__":
    unittest.main()
