package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/bacrehclb/ShadowTalk/internal/app"
	"github.com/bacrehclb/ShadowTalk/internal/identity"
	"github.com/bacrehclb/ShadowTalk/internal/storage/profilefile"
)

const usage = `ShadowTalk — локальный профиль

Использование:
  shadowtalk [--profile-dir PATH] profile create --nick NICK
  shadowtalk [--profile-dir PATH] profile show

Ник: 3–24 символа; латинские буквы, цифры, _ и -.
Первый и последний символ — буква или цифра.

--profile-dir задаётся перед командой profile.
По умолчанию используется каталог shadowtalk внутри системного каталога
конфигурации пользователя. --help показывает эту справку.`

func run(args []string, stdout, stderr io.Writer) int {
	if err := execute(args, stdout); err != nil && !errors.Is(err, flag.ErrHelp) {
		fmt.Fprintln(stderr, "Ошибка:", userError(err))
		return 1
	}
	return 0
}

func execute(args []string, stdout io.Writer) error {
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
	if args[0] != "profile" {
		return errors.New("неизвестная команда; используйте --help")
	}
	if len(args) == 1 || args[1] == "--help" || args[1] == "-h" {
		_, err := fmt.Fprintln(stdout, usage)
		return err
	}

	command := args[1]
	if command != "create" && command != "show" {
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

	if *directory == "" {
		configDirectory, err := os.UserConfigDir()
		if err != nil {
			return fmt.Errorf("не удалось определить каталог профиля; задайте --profile-dir: %w", err)
		}
		*directory = filepath.Join(configDirectory, "shadowtalk")
	}
	profiles := app.NewProfiles(profilefile.New(*directory))
	if command == "create" {
		profile, err := profiles.Create(nickname)
		if err != nil {
			return err
		}
		_, err = fmt.Fprintf(stdout, "Профиль создан: %s\n", profile.Nickname)
		return err
	}
	profile, err := profiles.Load()
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(stdout, "Ник: %s\nСоздан: %s\n", profile.Nickname, profile.CreatedAt.Format(time.RFC3339))
	return err
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
	default:
		return err.Error()
	}
}
