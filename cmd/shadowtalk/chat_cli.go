package main

import (
	"context"
	"errors"
	"flag"
	"io"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/bacrehclb/ShadowTalk/internal/app"
	"github.com/bacrehclb/ShadowTalk/internal/chat"
	"github.com/bacrehclb/ShadowTalk/internal/contacts"
	"github.com/bacrehclb/ShadowTalk/internal/delivery"
	"github.com/bacrehclb/ShadowTalk/internal/history"
	"github.com/bacrehclb/ShadowTalk/internal/identity"
	"github.com/bacrehclb/ShadowTalk/internal/peers"
	"github.com/bacrehclb/ShadowTalk/internal/storage/contactsfile"
	"github.com/bacrehclb/ShadowTalk/internal/storage/profiledir"
	"github.com/bacrehclb/ShadowTalk/internal/storage/sqlite"
	"github.com/bacrehclb/ShadowTalk/internal/transport/tor"
	"github.com/bacrehclb/ShadowTalk/internal/tui"
)

const chatUsage = `Использование:
  shadowtalk [--profile-dir PATH] chat [--contact NAME] [--tor-binary PATH] [--startup-timeout 3m]

Открывает терминальный чат с сохранёнными контактами. Нужны терминальные stdin
и stdout, окно от 60 × 20, существующий профиль и Tor (tor/tor.exe в PATH).
--tor-binary задаёт исполняемый файл Tor; загрузки Tor из интернета нет.
--startup-timeout ограничивает запуск Tor: от 1s до 15m, по умолчанию 3m.
--contact выбирает начальный диалог по локальному имени.

Пример: shadowtalk chat --contact alice
         shadowtalk chat --tor-binary "/path with spaces/tor"

Tab — контакты/ввод; стрелки — выбор контакта; Enter — отправить.
Alt+Enter — новая строка; PgUp/PgDn — история; Ctrl+L — последние сообщения.
Esc / Ctrl+C — остановить сеть и выйти. Ctrl+R — повторить сохранение после ошибки.
Очередь сохраняется до отправки и возобновляется при запуске chat.
«Доставлено» — собеседник подтвердил сохранение, не прочтение.
Изменения списка контактов применяются после перезапуска chat.
NO_COLOR отключает цвет. Pipe и TERM=dumb не поддерживаются; ошибки — код 1.
--help работает без профиля, файлов и сети.`

func executeChat(args []string, directory string, stdin io.Reader, stdout io.Writer, keys app.EncryptionKeys) error {
	flags := flag.NewFlagSet("chat", flag.ContinueOnError)
	alias := flags.String("contact", "", "начальный контакт")
	binary := flags.String("tor-binary", "tor", "исполняемый файл Tor")
	timeout := flags.Duration("startup-timeout", 3*time.Minute, "время запуска Tor")
	if err := parseContactFlags(flags, args, stdout, chatUsage); err != nil {
		return err
	}
	if *binary == "" || *timeout < time.Second || *timeout > 15*time.Minute {
		return errors.New("неверные параметры Tor; используйте chat --help")
	}
	if *alias != "" && identity.ValidateNickname(*alias) != nil {
		return errors.New("неверное имя контакта; используйте contacts list")
	}
	path, err := profileDirectory(directory)
	if err != nil {
		return err
	}
	input, _ := stdin.(*os.File)
	output, _ := stdout.(*os.File)
	return tui.RunChat(chatOpener(path, keys, *alias, tor.Options{BinaryPath: *binary, DataDir: filepath.Join(path, "tor"), StartTimeout: *timeout}), input, output, tui.ChatOptions{InitialContact: *alias, DescribeError: chatUserError})
}

type profileLoadFunc func() (app.LoadedProfile, error)

func (f profileLoadFunc) Load() (app.LoadedProfile, error) { return f() }

type contactReadFunc func() (*contacts.Book, error)

func (f contactReadFunc) Read() (*contacts.Book, error) { return f() }

func chatOpener(path string, keys app.EncryptionKeys, alias string, options tor.Options) tui.OpenChat {
	return func(ctx context.Context, stage func(tui.ChatStage)) (tui.ChatSession, error) {
		var profile identity.Profile
		var entries []contacts.Contact
		profiles := app.NewProfiles(profiledir.New(path), keys)
		p := app.NewPeerSessions(profileLoadFunc(func() (app.LoadedProfile, error) {
			stage(tui.ChatLoadingProfile)
			loaded, err := profiles.Load()
			profile = loaded.Profile
			return loaded, err
		}), contactReadFunc(func() (*contacts.Book, error) {
			book, err := contactsfile.New(path, profile.PublicKey).Read()
			if err != nil {
				return nil, err
			}
			if alias != "" {
				if _, err := book.Get(alias); err != nil {
					return nil, err
				}
			}
			entries = book.List()
			return book, nil
		}), func(lifetime context.Context, k identity.KeyPair) (peers.Transport, error) {
			stage(tui.ChatConnectingTor)
			return tor.Start(lifetime, k, options)
		})
		service, err := p.StartDelivery(ctx, func(ctx context.Context, owner [32]byte, key []byte) (delivery.Store, error) {
			stage(tui.ChatOpeningHistory)
			return sqlite.Open(ctx, path, owner, key)
		}, peers.Options{}, chat.Options{}, delivery.Options{})
		if err != nil {
			return nil, err
		}
		return &chatSession{service: service, profile: profile, contacts: entries}, nil
	}
}

type chatSession struct {
	service  *delivery.Service
	profile  identity.Profile
	contacts []contacts.Contact
}

func (s *chatSession) Profile() identity.Profile     { return s.profile }
func (s *chatSession) Contacts() []contacts.Contact  { return slices.Clone(s.contacts) }
func (s *chatSession) Activity() map[[32]byte]uint64 { return s.service.Activity() }
func (s *chatSession) Changes() <-chan struct{}      { return s.service.Changes() }
func (s *chatSession) Err() error                    { return s.service.Err() }
func (s *chatSession) Close() error                  { return s.service.Close() }
func (s *chatSession) Send(ctx context.Context, peer [32]byte, m chat.Message) error {
	_, _, err := s.service.Queue(ctx, peer, m)
	return err
}
func (s *chatSession) History(ctx context.Context, peer [32]byte, q history.Query) (tui.ChatPage, error) {
	page, err := s.service.List(ctx, peer, q)
	if err != nil {
		return tui.ChatPage{}, err
	}
	result := tui.ChatPage{NextBefore: page.NextBefore, Entries: make([]tui.ChatEntry, 0, len(page.Entries))}
	for _, entry := range page.Entries {
		row := tui.ChatEntry{Entry: entry}
		if entry.Direction == history.Outgoing {
			item, err := s.service.Status(ctx, peer, entry.ID)
			if err != nil && !errors.Is(err, history.ErrNotFound) {
				return tui.ChatPage{}, err
			}
			if err == nil {
				row.State = item.State
			}
		}
		result.Entries = append(result.Entries, row)
	}
	return result, nil
}

func chatUserError(err error) string {
	switch {
	case errors.Is(err, tor.ErrUnavailable):
		return "Tor не найден: установите Tor и задайте --tor-binary PATH либо добавьте его в PATH."
	case errors.Is(err, context.DeadlineExceeded):
		return "Tor или операция не завершились вовремя. Проверьте сеть; для запуска можно увеличить --startup-timeout."
	case errors.Is(err, tor.ErrInUse), errors.Is(err, history.ErrBusy):
		return "История или Tor заняты. Закройте другой экземпляр чата с этим профилем и повторите операцию."
	case errors.Is(err, identity.ErrInvalidKeyPair):
		return "Проверьте ключи командой profile show. Для старого профиля без ключей выполните profile upgrade."
	case errors.Is(err, tor.ErrDataDirectory), errors.Is(err, history.ErrUnsafePath):
		return "Небезопасный каталог данных. На Unix нужны права 0700 для каталога профиля и 0600 для его базы; проверьте также ссылки."
	case errors.Is(err, tor.ErrAuthentication), errors.Is(err, tor.ErrControl), errors.Is(err, tor.ErrProcessExited):
		return "Не удалось запустить или удержать Tor. Проверьте исполняемый файл и сеть, затем перезапустите чат."
	case errors.Is(err, history.ErrUnsupportedVersion):
		return "База создана более новой версией ShadowTalk. Обновите приложение."
	case errors.Is(err, history.ErrCorrupt), errors.Is(err, history.ErrDecryption), errors.Is(err, history.ErrOwnerMismatch), errors.Is(err, history.ErrForeignDatabase):
		return "Историю не удалось проверить. Сохраните исходные файлы и ключи; база не была сброшена."
	case errors.Is(err, history.ErrStorage):
		return "Не удалось сохранить или прочитать историю. Проверьте свободное место и доступ к каталогу профиля."
	case errors.Is(err, delivery.ErrQueueFull):
		return "Очередь исходящих заполнена. Дождитесь доставки сохранённых сообщений."
	case errors.Is(err, history.ErrConflict):
		return "Этот ID уже сохранён с другим текстом. Перезапустите чат и проверьте историю."
	case errors.Is(err, contacts.ErrContactNotFound), errors.Is(err, delivery.ErrUnknownPeer):
		return "Контакт не найден. Проверьте contacts list и перезапустите чат."
	case errors.Is(err, delivery.ErrClosed), errors.Is(err, delivery.ErrNetwork), errors.Is(err, peers.ErrTransport):
		return "Сеть чата остановлена. Сохранённая очередь продолжит работу при следующем запуске."
	case errors.Is(err, context.Canceled):
		return "Операция отменена."
	}
	// Reuse known profile diagnostics, but never expose arbitrary driver/network
	// error strings or plaintext through the terminal error boundary.
	description := userError(err)
	if description != err.Error() {
		return description
	}
	return "Не удалось выполнить операцию. Проверьте профиль, доступ к ключам и каталог данных."
}
