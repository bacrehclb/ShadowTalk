//go:build darwin || linux

package main

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/bacrehclb/ShadowTalk/internal/storage/torconfig"
)

func TestMenuTerminalNavigationAndSettings(t *testing.T) {
	profile := filepath.Join(t.TempDir(), "menu profile")
	p := startTerminalArgs(t, profile, t.TempDir(), false, false, false, nil, "menu")
	p.expect(0, "главное меню")
	if _, err := os.Stat(profile); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("opening menu touched profile")
	}
	mark := p.send("\r")
	p.expect(mark, "Добро пожаловать")
	mark = p.send("menu_peer\r")
	p.expect(mark, "Отпечаток публичного ключа")
	mark = p.send("\x1b")
	p.expect(mark, "главное меню")
	mark = p.send("\x1b[B\x1b[B\r")
	p.expect(mark, "Срок приглашения")
	mark = p.send("\r")
	p.expect(mark, "Передайте весь токен")
	mark = p.send("s")
	p.expect(mark, "Сохранить в новый файл")
	mark = p.send("\r")
	p.expect(mark, "Приглашение сохранено")
	if _, err := os.Stat(filepath.Join(profile, "contact.invite")); err != nil {
		t.Fatal(err)
	}
	mark = p.send("\x1b")
	p.expect(mark, "главное меню")
	mark = p.send("\x1b[B\x1b[B\x1b[B\x1b[B\x1b[B\r")
	p.expect(mark, "настройки Tor")
	mark = p.send("\r")
	p.expect(mark, "Ожидание Tor")
	mark = p.send("\x15" + "5m\r\r")
	p.expect(mark, "Настройки Tor сохранены")
	saved, err := torconfig.Read(filepath.Join(profile, torconfig.FileName))
	if err != nil || saved.StartTimeout.String() != "5m0s" {
		t.Fatal(saved, err)
	}
	p.send("\x03")
	p.finish(true)
}

func TestMenuRejectsRedirectedStreamsAndDumbTerminal(t *testing.T) {
	for _, mode := range []struct {
		name          string
		in, out, dumb bool
	}{{"stdin", true, false, false}, {"stdout", false, true, false}, {"dumb", false, false, true}} {
		t.Run(mode.name, func(t *testing.T) {
			profile := filepath.Join(t.TempDir(), "missing")
			p := startTerminalArgs(t, profile, t.TempDir(), mode.in, mode.out, mode.dumb, nil, "menu")
			p.finish(false)
			if _, err := os.Stat(profile); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("noninteractive menu touched profile")
			}
		})
	}
}
