package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/bacrehclb/ShadowTalk/internal/app"
	"github.com/bacrehclb/ShadowTalk/internal/identity"
	"github.com/bacrehclb/ShadowTalk/internal/storage/keyring"
	"github.com/bacrehclb/ShadowTalk/internal/storage/profiledir"
	"github.com/bacrehclb/ShadowTalk/internal/storage/profilefile"
	"github.com/bacrehclb/ShadowTalk/internal/storage/seedfile"
	"github.com/bacrehclb/ShadowTalk/internal/tui"
)

const usage = `ShadowTalk — локальный профиль

Использование:
  shadowtalk [--profile-dir PATH] ui
  shadowtalk [--profile-dir PATH] profile create --nick NICK
  shadowtalk [--profile-dir PATH] profile show
  shadowtalk [--profile-dir PATH] profile upgrade
  shadowtalk [--profile-dir PATH] profile recover

ui открывает терминальный интерфейс профиля (нужны терминальные stdin и stdout).
create создаёт профиль и сохраняет ключи в системном хранилище ОС.
show проверяет ключи и показывает отпечаток публичной идентичности.
upgrade добавляет ключи старому профилю без изменения ника и даты создания.
recover завершает прерванное создание с сохранёнными ключами.

Ник: 3–24 символа; латинские буквы, цифры, _ и -.
Первый и последний символ — буква или цифра.

--profile-dir задаётся перед командой ui или profile.
По умолчанию используется каталог shadowtalk внутри системного каталога
конфигурации пользователя. --help показывает эту справку.`

func run(args []string, stdout, stderr io.Writer) int {
	return runWithKeys(args, stdout, stderr, keyring.New())
}

func runWithKeys(args []string, stdout, stderr io.Writer, keys app.EncryptionKeys) int {
	if err := execute(args, stdout, keys); err != nil && !errors.Is(err, flag.ErrHelp) {
		fmt.Fprintln(stderr, "Ошибка:", userError(err))
		return 1
	}
	return 0
}

func execute(args []string, stdout io.Writer, keys app.EncryptionKeys) error {
	flags := flag.NewFlagSet("shadowtalk", flag.ContinueOnError)
	directory := flags.String("profile-dir", "", "каталог локального профиля")
	if err := parseFlags(flags, args, stdout); err != nil {
		return err
	}
	args = flags.Args()
	if len(args) == 0 {
		_, err := fmt.Fprintln(stdout, usage)
		return err
	}
	if args[0] != "profile" && args[0] != "ui" {
		return errors.New("неизвестная команда; используйте --help")
	}
	if args[0] == "ui" {
		uiFlags := flag.NewFlagSet("ui", flag.ContinueOnError)
		if err := parseFlags(uiFlags, args[1:], stdout); err != nil {
			return err
		}
		if uiFlags.NArg() != 0 {
			return errors.New("лишние аргументы; используйте ui --help")
		}
		path, err := profileDirectory(*directory)
		if err != nil {
			return err
		}
		output, _ := stdout.(*os.File)
		profiles := app.NewProfiles(profiledir.New(path), keys)
		return tui.Run(profileUI{profiles}, os.Stdin, output, tui.Options{Directory: path, DescribeError: userError})
	}
	if len(args) == 1 || args[1] == "--help" || args[1] == "-h" {
		_, err := fmt.Fprintln(stdout, usage)
		return err
	}

	command := args[1]
	if command != "create" && command != "show" && command != "upgrade" && command != "recover" {
		return errors.New("неизвестная команда профиля; используйте --help")
	}
	commandFlags := flag.NewFlagSet("profile "+command, flag.ContinueOnError)
	var nickname string
	if command == "create" {
		commandFlags.StringVar(&nickname, "nick", "", "ник профиля")
	}
	if err := parseFlags(commandFlags, args[2:], stdout); err != nil {
		return err
	}
	if commandFlags.NArg() != 0 {
		return errors.New("лишние аргументы; используйте --help")
	}

	path, err := profileDirectory(*directory)
	if err != nil {
		return err
	}
	profiles := app.NewProfiles(profiledir.New(path), keys)
	if command == "create" {
		profile, err := profiles.Create(nickname)
		if err != nil {
			return err
		}
		_, err = fmt.Fprintf(stdout, "Профиль создан: %s\n", profile.Profile.Nickname)
		return err
	}
	if command == "upgrade" || command == "recover" {
		var profile app.LoadedProfile
		var err error
		if command == "upgrade" {
			profile, err = profiles.Upgrade()
		} else {
			profile, err = profiles.Recover()
		}
		if err != nil {
			return err
		}
		_, err = fmt.Fprintf(stdout, "Идентичность готова: %s\nОтпечаток: %s\n", profile.Profile.Nickname, profile.Profile.Fingerprint())
		return err
	}
	profile, err := profiles.Load()
	if err != nil {
		return err
	}
	text := fmt.Sprintf("Ник: %s\nСоздан: %s\n", profile.Profile.Nickname, profile.Profile.CreatedAt.Format(time.RFC3339))
	if profile.Profile.HasIdentity() {
		text += "Отпечаток: " + profile.Profile.Fingerprint() + "\n"
	} else {
		text += "Ключи ещё не созданы; выполните profile upgrade.\n"
	}
	_, err = fmt.Fprint(stdout, text)
	return err
}

func profileDirectory(directory string) (string, error) {
	if directory != "" {
		return directory, nil
	}
	configDirectory, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("не удалось определить каталог профиля; задайте --profile-dir: %w", err)
	}
	return filepath.Join(configDirectory, "shadowtalk"), nil
}

// Parse errors belong on stderr; help goes to stdout only when requested.
func parseFlags(flags *flag.FlagSet, args []string, stdout io.Writer) error {
	flags.SetOutput(io.Discard)
	flags.Usage = func() {}
	err := flags.Parse(args)
	if errors.Is(err, flag.ErrHelp) {
		if _, writeErr := fmt.Fprintln(stdout, usage); writeErr != nil {
			return writeErr
		}
	}
	return err
}

func userError(err error) string {
	switch {
	case errors.Is(err, app.ErrProfileBusy):
		return "профиль используется другой операцией; повторите команду после её завершения"
	case errors.Is(err, app.ErrProfilePending):
		if errors.Is(err, keyring.ErrUnavailable) {
			return "создание не завершено: хранилище ключей недоступно; откройте доступ к нему и выполните profile recover"
		}
		return "создание профиля не завершено; выполните profile recover; если ошибка повторяется, сохраните каталог профиля для восстановления"
	case errors.Is(err, app.ErrNoPendingProfile):
		return "незавершённого создания нет; используйте profile show"
	case errors.Is(err, app.ErrCreationAborted):
		return "незавершённое создание отменено: ключ не был сохранён; повторите profile create или profile upgrade"
	case errors.Is(err, app.ErrProfileConflict), errors.Is(err, app.ErrIdentityMismatch):
		return "файлы профиля не соответствуют сохранённой идентичности; автоматическая замена ключей запрещена"
	case errors.Is(err, keyring.ErrWriteUnconfirmed):
		return "хранилище ОС не подтвердило запись ключа; проверьте доступ и повторите создание идентичности"
	case errors.Is(err, keyring.ErrNotFound):
		return "ключ шифрования профиля отсутствует в хранилище ОС; для восстановления нужен исходный ключ"
	case errors.Is(err, keyring.ErrUnavailable):
		return "нет доступа к системному хранилищу ключей; разблокируйте его и повторите команду (на Linux нужен Secret Service)"
	case errors.Is(err, keyring.ErrInvalidStoredKey), errors.Is(err, identity.ErrInvalidEncryptionKeySize), errors.Is(err, identity.ErrSeedDecryption), errors.Is(err, seedfile.ErrInvalidRecord):
		return "сохранённые ключи повреждены или не подходят к профилю; данные не были заменены"
	case errors.Is(err, seedfile.ErrUnsupportedVersion):
		return "версия файла ключей не поддерживается"
	case errors.Is(err, identity.ErrInvalidNickname):
		return "ник должен содержать 3–24 символа: латинские буквы, цифры, _ и -; по краям — буква или цифра"
	case errors.Is(err, identity.ErrProfileExists):
		return "профиль уже существует и не был изменён; используйте profile show"
	case errors.Is(err, identity.ErrProfileNotFound):
		return "профиль не найден; создайте его командой profile create --nick NICK"
	case errors.Is(err, identity.ErrInvalidProfile):
		return "файл профиля повреждён или содержит некорректные данные; файл не был изменён"
	case errors.Is(err, profilefile.ErrUnsupportedVersion):
		return "версия файла профиля не поддерживается; файл не был изменён"
	case errors.Is(err, fs.ErrPermission):
		return "нет доступа к файлам профиля; проверьте права на выбранный каталог"
	default:
		return err.Error()
	}
}
