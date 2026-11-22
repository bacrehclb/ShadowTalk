//go:build darwin || linux

package main

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/bacrehclb/ShadowTalk/internal/identity"
	"github.com/bacrehclb/ShadowTalk/internal/storage/profilefile"
	"github.com/charmbracelet/x/ansi"
	"github.com/creack/pty"
)

type terminalTest struct {
	t                       *testing.T
	master, slave           *os.File
	command                 *exec.Cmd
	done                    chan struct{}
	waitErr                 error
	diagnostic, pipedOutput bytes.Buffer
	mu                      sync.Mutex
	output                  strings.Builder
	changed                 chan struct{}
}

func startTerminal(t *testing.T, profile, keys string, inputPipe, outputPipe, dumb bool) *terminalTest {
	t.Helper()
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	master, slave, err := pty.Open()
	if err != nil {
		t.Fatal(err)
	}
	// Close the slave first so a blocking master read reaches EOF after the
	// child exits. Closing a still-blocking master first can deadlock cleanup.
	t.Cleanup(func() { _ = slave.Close(); _ = master.Close() })
	if err := pty.Setsize(master, &pty.Winsize{Rows: 24, Cols: 80}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(cancel)
	command := exec.CommandContext(ctx, binary, "-test.run=^TestCLIChildProcess$", "--", "--profile-dir", profile, "ui")
	terminal := &terminalTest{t: t, master: master, slave: slave, command: command, done: make(chan struct{}), changed: make(chan struct{}, 1)}
	command.Dir = t.TempDir()
	command.Env = append(os.Environ(), "SHADOWTALK_TEST_CHILD=1", "SHADOWTALK_TEST_TERMINAL=1", "SHADOWTALK_TEST_KEYS="+keys, "TERM=xterm-256color", "NO_COLOR=1")
	if dumb {
		command.Env = append(command.Env, "TERM=dumb")
	}
	command.Stdin, command.Stdout, command.Stderr = slave, slave, &terminal.diagnostic
	if inputPipe {
		command.Stdin = strings.NewReader("")
	}
	if outputPipe {
		command.Stdout = &terminal.pipedOutput
	}
	command.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true}
	// Setctty uses the first terminal descriptor in the child.
	if inputPipe {
		command.SysProcAttr.Ctty = 1
	}
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	go func() { terminal.waitErr = command.Wait(); close(terminal.done) }()
	t.Cleanup(func() { _ = command.Process.Kill(); <-terminal.done })
	go func() {
		buffer := make([]byte, 4096)
		for {
			n, err := master.Read(buffer)
			if n > 0 {
				terminal.mu.Lock()
				if terminal.output.Len() < 1<<20 {
					terminal.output.Write(buffer[:n])
				}
				terminal.mu.Unlock()
				select {
				case terminal.changed <- struct{}{}:
				default:
				}
			}
			if err != nil {
				return
			}
		}
	}()
	return terminal
}

func (p *terminalTest) snapshot() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.output.String()
}

func (p *terminalTest) expect(after int, fragment string) {
	p.t.Helper()
	deadline := time.NewTimer(10 * time.Second)
	defer deadline.Stop()
	for {
		if strings.Contains(ansi.Strip(p.snapshot()[after:]), fragment) {
			return
		}
		select {
		case <-p.changed:
		case <-p.done:
			p.t.Fatalf("terminal exited before %q: %v; stderr=%s; output=%q", fragment, p.waitErr, p.diagnostic.String(), p.snapshot())
		case <-deadline.C:
			p.t.Fatalf("terminal did not display %q; output=%q", fragment, p.snapshot())
		}
	}
}

func (p *terminalTest) send(keys string) int {
	p.t.Helper()
	mark := len(p.snapshot())
	if _, err := p.master.WriteString(keys); err != nil {
		p.t.Fatal(err)
	}
	return mark
}

func (p *terminalTest) finish(success bool) {
	p.t.Helper()
	<-p.done
	if (p.waitErr == nil) != success {
		p.t.Fatalf("exit=%v stderr=%s", p.waitErr, p.diagnostic.String())
	}
	if !success {
		var exit *exec.ExitError
		if !errors.As(p.waitErr, &exit) || exit.ExitCode() != 1 {
			p.t.Fatalf("unexpected terminal exit: %v", p.waitErr)
		}
	}
	if success && p.diagnostic.Len() != 0 {
		p.t.Fatalf("unexpected stderr: %s", p.diagnostic.String())
	}
}

func TestUIInTerminalCreatesRestartsAndRestoresTerminal(t *testing.T) {
	profile, keys := filepath.Join(t.TempDir(), "профиль с пробелами"), t.TempDir()
	p := startTerminal(t, profile, keys, false, false, false)
	p.expect(0, "Добро пожаловать")
	mark := p.send("bad.name\r")
	p.expect(mark, "Ник:")
	mark = p.send("\x15ivan_01\r")
	p.expect(mark, "Отпечаток публичного ключа")
	saved, err := profilefile.New(profile).Load()
	if err != nil || !saved.HasIdentity() || saved.Nickname != "ivan_01" {
		t.Fatalf("saved profile: %+v %v", saved, err)
	}
	mark = len(p.snapshot())
	if err := pty.Setsize(p.master, &pty.Winsize{Rows: 10, Cols: 30}); err != nil {
		t.Fatal(err)
	}
	p.expect(mark, "Увеличьте окно")
	mark = len(p.snapshot())
	if err := pty.Setsize(p.master, &pty.Winsize{Rows: 24, Cols: 80}); err != nil {
		t.Fatal(err)
	}
	p.expect(mark, "Отпечаток публичного ключа")
	p.send("\x1b")
	p.finish(true)
	assertNoColorSGR(t, p.snapshot())

	restarted := startTerminal(t, profile, keys, false, false, false)
	restarted.expect(0, saved.Fingerprint())
	restarted.send("\x03")
	restarted.finish(true)
	entries, err := os.ReadDir(keys)
	if err != nil || len(entries) != 1 {
		t.Fatalf("restart generated extra keys: %v", err)
	}
}

func TestUIInTerminalUpgradesLegacyProfile(t *testing.T) {
	profile, keys := t.TempDir(), t.TempDir()
	old := identity.Profile{Nickname: "old_nick", CreatedAt: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)}
	if err := profilefile.New(profile).Create(old); err != nil {
		t.Fatal(err)
	}
	p := startTerminal(t, profile, keys, false, false, false)
	p.expect(0, "Добавьте ключи профиля")
	mark := p.send("\r")
	p.expect(mark, "Отпечаток публичного ключа")
	p.send("\x1b")
	p.finish(true)
	saved, err := profilefile.New(profile).Load()
	if err != nil || !saved.HasIdentity() || saved.Nickname != old.Nickname || !saved.CreatedAt.Equal(old.CreatedAt) {
		t.Fatalf("upgrade changed metadata: %+v %v", saved, err)
	}
}

func TestUIInTerminalRecoversInterruptedCreation(t *testing.T) {
	work, keys := t.TempDir(), t.TempDir()
	profile := filepath.Join(work, "profile")
	crashed := runCLIProcess(t, work, keys, []string{"SHADOWTALK_TEST_CRASH=1"}, "--profile-dir", profile, "profile", "create", "--nick", "recover_me")
	if crashed.code != 90 {
		t.Fatal(crashed)
	}
	p := startTerminal(t, profile, keys, false, false, false)
	p.expect(0, "Создание было прервано")
	mark := p.send("\r")
	p.expect(mark, "Отпечаток публичного ключа")
	p.send("\x03")
	p.finish(true)
	if _, err := os.Stat(filepath.Join(profile, "profile-pending.json")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatal("journal was not completed")
	}
	entries, err := os.ReadDir(keys)
	if err != nil || len(entries) != 1 {
		t.Fatal("recovery created another identity")
	}
}

func TestUIRejectsUnsupportedTerminalsWithoutSideEffects(t *testing.T) {
	for _, mode := range []string{"stdin-pipe", "stdout-pipe", "dumb"} {
		t.Run(mode, func(t *testing.T) {
			profile := filepath.Join(t.TempDir(), "missing")
			p := startTerminal(t, profile, t.TempDir(), mode == "stdin-pipe", mode == "stdout-pipe", mode == "dumb")
			p.finish(false)
			if p.diagnostic.Len() == 0 || p.pipedOutput.Len() != 0 || p.snapshot() != "" {
				t.Fatal("unsupported terminal produced UI output")
			}
			if _, err := os.Stat(profile); !errors.Is(err, fs.ErrNotExist) {
				t.Fatal("unsupported terminal created profile")
			}
		})
	}
}

func assertNoColorSGR(t *testing.T, output string) {
	t.Helper()
	for _, sequence := range regexp.MustCompile("\x1b\\[([0-9;:]*)m").FindAllStringSubmatch(output, -1) {
		for _, parameter := range strings.FieldsFunc(sequence[1], func(r rune) bool { return r == ';' || r == ':' }) {
			code, _ := strconv.Atoi(parameter)
			if code >= 30 && code <= 48 || code >= 90 && code <= 107 || code == 58 {
				t.Fatalf("NO_COLOR emitted color: %q", sequence[0])
			}
		}
	}
}
