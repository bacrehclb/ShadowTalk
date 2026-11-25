package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/bacrehclb/ShadowTalk/internal/app"
	"github.com/bacrehclb/ShadowTalk/internal/contacts"
	"github.com/bacrehclb/ShadowTalk/internal/identity"
	"github.com/bacrehclb/ShadowTalk/internal/storage/contactsfile"
	"github.com/bacrehclb/ShadowTalk/internal/storage/profiledir"
)

const inviteUsage = `Использование:
  shadowtalk [--profile-dir PATH] invite create [--ttl 24h]
  shadowtalk invite inspect --file PATH|-

create печатает только подписанный токен и перевод строки в stdout.
Срок: от 1s до 168h, целые секунды; по умолчанию 24h. Tor не запускается.
inspect проверяет токен и показывает ник, onion-адрес, полный отпечаток и сроки.
--file - читает stdin до EOF; файл/поток должен содержать ровно один токен.
Пример: shadowtalk invite create > alice.invite
         shadowtalk invite inspect --file alice.invite

Подпись не доказывает личность: сверьте отпечаток по доверенному каналу.
Для взаимного доверия участники обмениваются приглашениями в обе стороны.
Токен не содержит приватных ключей. Одноразовое принятие и отзыв пока не реализованы.`

const contactsUsage = `Использование:
  shadowtalk [--profile-dir PATH] contacts add --alias NAME --fingerprint SHA256:... --file PATH|-
  shadowtalk [--profile-dir PATH] contacts list
  shadowtalk [--profile-dir PATH] contacts show --alias NAME
  shadowtalk [--profile-dir PATH] contacts remove --alias NAME --fingerprint SHA256:...

add повторно проверяет приглашение и сохраняет контакт после точного совпадения
полного подтверждённого отпечатка. Получите его у собеседника по доверенному каналу.
Пример: shadowtalk contacts add --alias alice --fingerprint SHA256:... --file alice.invite
--file - читает токен из stdin до EOF. Токен не передаётся аргументом команды.
Локальное имя: 3–24 символа, правила как у ника; регистр имеет значение.
Повторный add того же контакта ничего не меняет. Другой ключ не заменяет старый.
list/show не читают приватные ключи. Пустой список выводится как «Контактов нет.».
remove удаляет только локальный контакт с указанным именем и отпечатком;
это не отзыв уже переданных приглашений и не блокировка удалённого пользователя.
Команды работают локально, без запуска Tor. Ошибки завершаются кодом 1.`

func parseContactFlags(flags *flag.FlagSet, args []string, stdout io.Writer, help string) error {
	flags.SetOutput(io.Discard)
	flags.Usage = func() {}
	err := flags.Parse(args)
	if errors.Is(err, flag.ErrHelp) {
		if _, writeErr := fmt.Fprintln(stdout, help); writeErr != nil {
			return writeErr
		}
	}
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return err
		}
		// Flag errors can echo user-supplied values; keep token bytes out of them.
		return errors.New("неверные параметры; используйте --help для этой команды")
	}
	if flags.NArg() != 0 {
		return errors.New("лишние аргументы; используйте --help")
	}
	return nil
}

func executeInvite(args []string, directory string, stdin io.Reader, stdout io.Writer, keys app.EncryptionKeys) error {
	if len(args) == 0 || args[0] == "--help" || args[0] == "-h" {
		_, err := fmt.Fprintln(stdout, inviteUsage)
		return err
	}
	flags := flag.NewFlagSet("invite "+args[0], flag.ContinueOnError)
	switch args[0] {
	case "create":
		lifetime := flags.Duration("ttl", contacts.DefaultLifetime, "срок приглашения")
		if err := parseContactFlags(flags, args[1:], stdout, inviteUsage); err != nil {
			return err
		}
		if err := contacts.ValidateLifetime(*lifetime); err != nil {
			return err
		}
		path, err := profileDirectory(directory)
		if err != nil {
			return err
		}
		loaded, err := app.NewProfiles(profiledir.New(path), keys).Load()
		if err != nil {
			return err
		}
		if !loaded.Profile.HasIdentity() {
			return errProfileNeedsIdentity
		}
		token, err := contacts.CreateInvitation(loaded.Keys, loaded.Profile.Nickname, time.Now(), *lifetime)
		if err != nil {
			return err
		}
		_, err = fmt.Fprintln(stdout, token)
		return err
	case "inspect":
		file := flags.String("file", "", "файл токена или - для stdin")
		if err := parseContactFlags(flags, args[1:], stdout, inviteUsage); err != nil {
			return err
		}
		token, err := readInvitation(*file, stdin)
		if err != nil {
			return err
		}
		invite, err := contacts.ParseInvitation(token, time.Now())
		if err != nil {
			return err
		}
		_, err = fmt.Fprintf(stdout, "Ник: %s\nOnion: %s\nОтпечаток: %s\nВыдано: %s\nДействует до: %s\nПодпись проверена. Личность собеседника ещё нужно подтвердить.\n", invite.Nickname, invite.Onion, invite.Fingerprint(), invite.IssuedAt.Format(time.RFC3339), invite.ExpiresAt.Format(time.RFC3339))
		return err
	default:
		return errors.New("неизвестная команда приглашений; используйте invite --help")
	}
}

func executeContacts(args []string, directory string, stdin io.Reader, stdout io.Writer) error {
	if len(args) == 0 || args[0] == "--help" || args[0] == "-h" {
		_, err := fmt.Fprintln(stdout, contactsUsage)
		return err
	}
	command := args[0]
	if command != "add" && command != "list" && command != "show" && command != "remove" {
		return errors.New("неизвестная команда контактов; используйте contacts --help")
	}
	flags := flag.NewFlagSet("contacts "+command, flag.ContinueOnError)
	var alias, fingerprint, file string
	if command != "list" {
		flags.StringVar(&alias, "alias", "", "локальное имя контакта")
	}
	if command == "add" || command == "remove" {
		flags.StringVar(&fingerprint, "fingerprint", "", "полный подтверждённый отпечаток")
	}
	if command == "add" {
		flags.StringVar(&file, "file", "", "файл токена или - для stdin")
	}
	if err := parseContactFlags(flags, args[1:], stdout, contactsUsage); err != nil {
		return err
	}
	if command != "list" && identity.ValidateNickname(alias) != nil {
		return errors.New("задайте --alias с локальным именем по правилам ника")
	}
	if (command == "add" || command == "remove") && fingerprint == "" {
		return contacts.ErrConfirmationRequired
	}
	var token string
	if command == "add" {
		var err error
		token, err = readInvitation(file, stdin)
		if err != nil {
			return err
		}
		// Reject bad input before reading the local profile or touching its files.
		if _, err = contacts.ConfirmInvitation(token, alias, fingerprint, time.Now()); err != nil {
			return err
		}
	}
	path, err := profileDirectory(directory)
	if err != nil {
		return err
	}
	data, err := profiledir.New(path).Load()
	if err != nil {
		return err
	}
	if !data.Profile.HasIdentity() {
		return errProfileNeedsIdentity
	}
	book := app.NewContacts(contactsfile.New(path, data.Profile.PublicKey))
	switch command {
	case "add":
		contact, err := book.Import(token, alias, fingerprint, time.Now())
		if err != nil {
			return err
		}
		_, err = fmt.Fprintf(stdout, "Контакт сохранён: %s\nОтпечаток: %s\n", contact.Alias, contact.Fingerprint())
		return err
	case "list":
		entries, err := book.List()
		if err != nil {
			return err
		}
		if len(entries) == 0 {
			_, err = fmt.Fprintln(stdout, "Контактов нет.")
			return err
		}
		var output strings.Builder
		for _, contact := range entries {
			fmt.Fprintf(&output, "%s\t%s\n", contact.Alias, contact.Fingerprint())
		}
		_, err = io.WriteString(stdout, output.String())
		return err
	case "show":
		contact, err := book.Get(alias)
		if err != nil {
			return err
		}
		_, err = fmt.Fprintf(stdout, "Имя: %s\nНик: %s\nOnion: %s\nОтпечаток: %s\nДобавлен: %s\n", contact.Alias, contact.Nickname, contact.Onion, contact.Fingerprint(), contact.AddedAt.Format(time.RFC3339))
		return err
	case "remove":
		if err := book.Remove(alias, fingerprint); err != nil {
			return err
		}
		_, err = fmt.Fprintf(stdout, "Контакт удалён: %s\n", alias)
		return err
	}
	return nil
}

func readInvitation(path string, stdin io.Reader) (string, error) {
	if path == "" {
		return "", errors.New("задайте --file PATH или --file - для чтения stdin")
	}
	input := stdin
	if path != "-" {
		info, err := os.Stat(path)
		if err != nil || !info.Mode().IsRegular() {
			return "", errors.New("не удалось прочитать обычный файл приглашения")
		}
		file, err := os.Open(path)
		if err != nil {
			return "", errors.New("не удалось открыть файл приглашения")
		}
		defer file.Close()
		input = file
	}
	data, err := io.ReadAll(io.LimitReader(input, contacts.MaxTokenSize+3))
	if err != nil {
		return "", errors.New("не удалось прочитать приглашение")
	}
	// Accept one LF or CRLF from shell redirection, but no arbitrary whitespace.
	token := string(data)
	if strings.HasSuffix(token, "\r\n") {
		token = strings.TrimSuffix(token, "\r\n")
	} else {
		token = strings.TrimSuffix(token, "\n")
	}
	if len(token) > contacts.MaxTokenSize {
		return "", contacts.ErrInvalidInvitation
	}
	return token, nil
}

var errProfileNeedsIdentity = errors.New("profile needs identity")

func contactError(err error) string {
	switch {
	case errors.Is(err, errProfileNeedsIdentity):
		return "у профиля ещё нет ключей; выполните profile upgrade"
	case errors.Is(err, contacts.ErrInvalidInvitation):
		return "приглашение повреждено, имеет неподдерживаемый формат или неверную подпись; запросите новый токен"
	case errors.Is(err, contacts.ErrExpiredInvitation):
		return "срок приглашения истёк; запросите новый токен"
	case errors.Is(err, contacts.ErrFutureInvitation):
		return "приглашение выдано в будущем; проверьте часы обоих устройств"
	case errors.Is(err, contacts.ErrInvalidLifetime):
		return "--ttl должен быть от 1s до 168h, целое число секунд"
	case errors.Is(err, contacts.ErrConfirmationRequired):
		return "укажите полный подтверждённый отпечаток через --fingerprint SHA256:..."
	case errors.Is(err, contacts.ErrFingerprintMismatch):
		return "отпечаток не совпадает; сверьте его с собеседником, контакт не изменён"
	case errors.Is(err, contacts.ErrKeyChanged):
		return "под этим именем уже сохранён другой ключ; автоматическая замена запрещена, сначала проверьте изменение у собеседника"
	case errors.Is(err, contacts.ErrContactChanged):
		return "данные известного контакта изменились; сохранённая запись не была заменена"
	case errors.Is(err, contacts.ErrDuplicateContact):
		return "этот ключ или onion-адрес уже сохранён под другим именем"
	case errors.Is(err, contacts.ErrSelfContact):
		return "нельзя добавить собственный профиль в контакты"
	case errors.Is(err, contacts.ErrContactNotFound):
		return "контакт не найден; используйте contacts list"
	case errors.Is(err, contacts.ErrInvalidContact):
		return "некорректные данные контакта; проверьте имя, ключ и onion-адрес"
	case errors.Is(err, contacts.ErrContactLimit):
		return "достигнут лимит 1000 контактов"
	case errors.Is(err, contactsfile.ErrBusy):
		return "контакты используются другой операцией; повторите после её завершения"
	case errors.Is(err, contactsfile.ErrOwnerMismatch):
		return "файл контактов принадлежит другой идентичности; автоматическая замена запрещена"
	case errors.Is(err, contactsfile.ErrUnsupportedVersion):
		return "версия файла контактов не поддерживается; файл не изменён"
	case errors.Is(err, contactsfile.ErrInvalidRecord):
		return "файл контактов повреждён; сохраните его для восстановления, автоматического сброса нет"
	}
	return ""
}
