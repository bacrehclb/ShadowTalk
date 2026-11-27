package main

import (
	"context"
	"errors"
	"flag"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/bacrehclb/ShadowTalk/internal/app"
	"github.com/bacrehclb/ShadowTalk/internal/contacts"
	"github.com/bacrehclb/ShadowTalk/internal/identity"
	"github.com/bacrehclb/ShadowTalk/internal/storage/contactsfile"
	"github.com/bacrehclb/ShadowTalk/internal/storage/profiledir"
	"github.com/bacrehclb/ShadowTalk/internal/storage/torconfig"
	"github.com/bacrehclb/ShadowTalk/internal/transport/tor"
	"github.com/bacrehclb/ShadowTalk/internal/tui"
	"github.com/charmbracelet/x/term"
)

const menuUsage = `Использование: shadowtalk [--profile-dir PATH] menu

Профиль, чат, приглашения, контакты и настройки Tor в одном меню.
Стрелки — выбор; Enter — открыть; Tab — поле; Esc — назад; Ctrl+C — выход.
В списке мостов Enter добавляет строку, Ctrl+S продолжает настройку.
Нужен терминал от 60 × 20. Без аргументов в терминале также открывается меню.
Команды ui, chat, profile, invite и contacts продолжают работать отдельно.
Меню не запускает Tor, пока вы не откроете чат.`

func menuTerminal(stdin io.Reader, stdout io.Writer) bool {
	input, iok := stdin.(*os.File)
	output, ook := stdout.(*os.File)
	return iok && ook && term.IsTerminal(input.Fd()) && term.IsTerminal(output.Fd()) && os.Getenv("TERM") != "dumb"
}

func executeMenu(args []string, directory string, stdin io.Reader, stdout io.Writer, keys app.EncryptionKeys) error {
	flags := flag.NewFlagSet("menu", flag.ContinueOnError)
	if err := parseContactFlags(flags, args, stdout, menuUsage); err != nil {
		return err
	}
	path, err := profileDirectory(directory)
	if err != nil {
		return err
	}
	path, err = filepath.Abs(path)
	if err != nil {
		return err
	}
	input, _ := stdin.(*os.File)
	output, _ := stdout.(*os.File)
	service := &menuService{directory: path, keys: keys}
	options := tui.MenuOptions{Directory: path, DescribeError: menuError}
	if runtime.GOOS == "darwin" {
		options.CopyText = copyMacClipboard
	}
	for {
		selection, err := tui.RunMenu(service, input, output, options)
		if err != nil {
			return err
		}
		options.Notice = ""
		switch selection.Action {
		case tui.MenuExit:
			return nil
		case tui.MenuProfile:
			err = tui.Run(profileUI{app.NewProfiles(profiledir.New(path), keys)}, input, output, tui.Options{Directory: path, DescribeError: userError})
		case tui.MenuChat:
			var args []string
			if selection.Contact != "" {
				args = []string{"--contact", selection.Contact}
			}
			err = executeChat(args, path, input, output, keys)
		}
		if err != nil {
			options.Notice = err.Error()
		}
	}
}

// menuService is application wiring, not a second command parser. CLI and menu
// share profile, invitation, pinning and Tor configuration validation.
type menuService struct {
	directory   string
	keys        app.EncryptionKeys
	torPrevious *torconfig.Settings
	torLoaded   bool
}

func (s *menuService) CreateInvitation(ttl time.Duration) (string, error) {
	if err := contacts.ValidateLifetime(ttl); err != nil {
		return "", err
	}
	loaded, err := app.NewProfiles(profiledir.New(s.directory), s.keys).Load()
	if err != nil {
		return "", err
	}
	if !loaded.Profile.HasIdentity() {
		return "", errProfileNeedsIdentity
	}
	return contacts.CreateInvitation(loaded.Keys, loaded.Profile.Nickname, time.Now(), ttl)
}

func (s *menuService) contactBook() (*app.Contacts, error) {
	data, err := profiledir.New(s.directory).Load()
	if err != nil {
		return nil, err
	}
	if !data.Profile.HasIdentity() {
		return nil, errProfileNeedsIdentity
	}
	return app.NewContacts(contactsfile.New(s.directory, data.Profile.PublicKey)), nil
}

func (s *menuService) ImportContact(token, alias, fingerprint string) error {
	if _, err := contacts.ConfirmInvitation(token, alias, fingerprint, time.Now()); err != nil {
		return err
	}
	book, err := s.contactBook()
	if err != nil {
		return err
	}
	_, err = book.Import(token, alias, fingerprint, time.Now())
	return err
}
func (s *menuService) ListContacts() ([]contacts.Contact, error) {
	book, err := s.contactBook()
	if err != nil {
		return nil, err
	}
	return book.List()
}
func (s *menuService) RemoveContact(alias, fingerprint string) error {
	book, err := s.contactBook()
	if err != nil {
		return err
	}
	return book.Remove(alias, fingerprint)
}

func (s *menuService) LoadTor() (tui.TorSettings, error) {
	s.torLoaded = false
	s.torPrevious = nil
	settings, err := torconfig.Read(filepath.Join(s.directory, torconfig.FileName))
	if errors.Is(err, os.ErrNotExist) {
		settings = torconfig.Defaults()
	} else if err != nil {
		return tui.TorSettings{}, err
	} else {
		s.torPrevious = &settings
	}
	s.torLoaded = true
	mode := settings.Connection.Mode
	if mode == "" {
		mode = "direct"
	}
	torBinary, lyrebird := settings.BinaryPath, settings.Connection.LyrebirdBinary
	if torBinary == "tor" {
		torBinary = ""
	}
	if lyrebird == "lyrebird" {
		lyrebird = ""
	}
	return tui.TorSettings{Mode: mode, Timeout: settings.StartTimeout.String(), Bridges: strings.Join(settings.Connection.Bridges, "\n"), TorBinary: torBinary, LyrebirdBinary: lyrebird}, nil
}

func (s *menuService) SaveTor(input tui.TorSettings) error {
	if !s.torLoaded {
		return torconfig.ErrChanged
	}
	timeout, err := time.ParseDuration(input.Timeout)
	if err != nil || timeout < time.Second || timeout > 15*time.Minute {
		return errMenuTimeout
	}
	binary := strings.TrimSpace(input.TorBinary)
	if binary == "" {
		binary = "tor"
	} else if !filepath.IsAbs(binary) {
		return errMenuBinary
	}
	connection := tor.Connection{Mode: input.Mode}
	if input.Mode == "obfs4" {
		connection.LyrebirdBinary = strings.TrimSpace(input.LyrebirdBinary)
		if connection.LyrebirdBinary == "" {
			connection.LyrebirdBinary = "lyrebird"
		} else if !filepath.IsAbs(connection.LyrebirdBinary) {
			return errMenuBinary
		}
		for _, line := range strings.Split(input.Bridges, "\n") {
			line = strings.TrimSpace(line)
			line = strings.TrimPrefix(line, "Bridge ")
			if line != "" {
				connection.Bridges = append(connection.Bridges, strings.Join(strings.Fields(line), " "))
			}
		}
	}
	if connection.Validate() != nil {
		return errMenuBridges
	}
	next := torconfig.Settings{BinaryPath: binary, StartTimeout: timeout, Connection: connection}
	if err := torconfig.Write(filepath.Join(s.directory, torconfig.FileName), s.torPrevious, next); err != nil {
		return err
	}
	// A fresh read is required before another edit, including after failures.
	s.torLoaded = false
	return nil
}

func (s *menuService) SaveInvitation(path, token string) error {
	if _, err := contacts.ParseInvitation(token, time.Now()); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if errors.Is(err, os.ErrExist) {
		return errMenuFileExists
	}
	if err != nil {
		return errMenuExport
	}
	_, writeErr := io.WriteString(f, token+"\n")
	syncErr := f.Sync()
	closeErr := f.Close()
	if errors.Join(writeErr, syncErr, closeErr) != nil {
		_ = os.Remove(path)
		return errMenuExport
	}
	return nil
}

func (s *menuService) ReadInvitation(path string) (string, error) {
	if path == "-" {
		return "", contacts.ErrInvalidInvitation
	}
	return readInvitation(path, strings.NewReader(""))
}

func copyMacClipboard(text string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "/usr/bin/pbcopy")
	cmd.Stdin = strings.NewReader(text)
	cmd.WaitDelay = time.Second
	if cmd.Run() != nil {
		return errMenuClipboard
	}
	return nil
}

var (
	errMenuClipboard  = errors.New("не удалось скопировать; нажмите s, чтобы сохранить приглашение в файл")
	errMenuTimeout    = errors.New("ожидание Tor должно быть от 1s до 15m; например 5m")
	errMenuBinary     = errors.New("укажите абсолютный путь исполняемого файла или оставьте поле пустым")
	errMenuBridges    = errors.New("нужны 1–16 разных полных строк obfs4: адрес, отпечаток, cert и iat-mode")
	errMenuFileExists = errors.New("файл уже существует; выберите другое имя, перезаписи не было")
	errMenuExport     = errors.New("не удалось сохранить приглашение; проверьте каталог и права записи")
)

func menuError(err error) string {
	switch {
	case errors.Is(err, identity.ErrProfileNotFound):
		return "Сначала создайте профиль: откройте пункт «Профиль» в главном меню."
	case errors.Is(err, errProfileNeedsIdentity):
		return "Откройте пункт «Профиль» и завершите создание ключей."
	case errors.Is(err, torconfig.ErrChanged):
		return "Настройки изменились в другом окне. Вернитесь в главное меню и откройте настройки Tor заново."
	case errors.Is(err, torconfig.ErrWrite):
		return "Не удалось сохранить настройки Tor. Проверьте доступ к каталогу профиля."
	case errors.Is(err, torconfig.ErrBusy):
		return "Настройки сохраняются другим процессом. Повторите позже."
	case errors.Is(err, errMenuTimeout), errors.Is(err, errMenuBinary), errors.Is(err, errMenuBridges), errors.Is(err, errMenuFileExists), errors.Is(err, errMenuExport), errors.Is(err, errMenuClipboard):
		return err.Error()
	}
	return chatUserError(err)
}
