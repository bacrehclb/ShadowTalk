package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/bacrehclb/ShadowTalk/internal/storage/torconfig"
	"github.com/bacrehclb/ShadowTalk/internal/transport/tor"
	"github.com/bacrehclb/ShadowTalk/internal/tui"
)

type chatNetworkFlags struct {
	config, binary        string
	timeout               time.Duration
	binarySet, timeoutSet bool
}

func (f chatNetworkFlags) options(directory string) (tor.Options, error) {
	path := f.config
	if path == "" {
		path = filepath.Join(directory, torconfig.FileName)
	}
	saved, err := torconfig.Read(path)
	if err != nil && !(f.config == "" && errors.Is(err, os.ErrNotExist)) {
		if errors.Is(err, os.ErrNotExist) {
			err = torconfig.ErrRead
		}
		return tor.Options{}, err
	}
	if err != nil {
		saved = torconfig.Settings{BinaryPath: "tor", StartTimeout: 3 * time.Minute}
	}
	if f.binarySet {
		saved.BinaryPath = f.binary
	}
	if f.timeoutSet {
		saved.StartTimeout = f.timeout
	}
	return tor.Options{BinaryPath: saved.BinaryPath, DataDir: filepath.Join(directory, "tor"), StartTimeout: saved.StartTimeout, Connection: saved.Connection}, nil
}

func torProgressText(p tor.Progress) string {
	switch p.Phase {
	case tor.StartingProcess:
		return "запуск процесса"
	case tor.StartingTransport:
		return "запуск obfs4"
	case tor.ConnectingController:
		return "проверка локального контроллера"
	case tor.PublishingService:
		return "публикация onion-службы"
	case tor.Ready:
		return "готово"
	}
	stage := "подготовка сети"
	switch p.Step {
	case tor.BootstrapTransport:
		stage = "подключение транспорта"
	case tor.BootstrapConnecting:
		stage = "соединение с ретранслятором"
	case tor.BootstrapHandshake:
		stage = "рукопожатие с ретранслятором"
	case tor.BootstrapDirectory:
		stage = "загрузка данных сети"
	case tor.BootstrapCircuit:
		stage = "построение цепочки"
	case tor.BootstrapDone:
		stage = "сеть готова"
	}
	switch p.Problem {
	case tor.BootstrapWarning:
		stage += "; Tor сообщает о проблеме"
	case tor.BootstrapTimeout:
		stage += "; нет ответа"
	case tor.BootstrapRefused:
		stage += "; соединение отклонено"
	case tor.BootstrapUnreachable:
		stage += "; соединение прервано или недоступно"
	case tor.BootstrapTransportFailed:
		stage += "; транспорт недоступен"
	}
	return stage
}

func chatTorProgress(p tor.Progress) tui.ChatStartup {
	return tui.ChatStartup{Stage: tui.ChatConnectingTor, Detail: torProgressText(p), Percent: p.Percent, HasPercent: p.Phase == tor.Bootstrapping || p.Phase == tor.PublishingService}
}

func torConnectionError(err error) string {
	switch {
	case errors.Is(err, torconfig.ErrUnsafeFile):
		return "Небезопасный tor.json: нужен обычный файл; на Unix задайте права 0600."
	case errors.Is(err, torconfig.ErrVersion):
		return "Версия tor.json не поддерживается. Для этой версии приложения нужен формат 1."
	case errors.Is(err, torconfig.ErrRead):
		return "Не удалось прочитать конфигурацию Tor. Проверьте --tor-config и доступ к файлу."
	case errors.Is(err, torconfig.ErrInvalid), errors.Is(err, tor.ErrInvalidConnection):
		return "Неверная конфигурация Tor. Проверьте mode, пути и полные строки мостов obfs4 в tor.json."
	case errors.Is(err, tor.ErrObfs4Unavailable):
		return "Lyrebird не найден или не запускается. Укажите исполняемый файл в lyrebird_binary внутри tor.json."
	case errors.Is(err, tor.ErrObfs4Startup):
		return "Lyrebird не подтвердил готовность obfs4. Проверьте бинарник из Tor Expert Bundle."
	case errors.Is(err, tor.ErrObfs4Exited):
		return "Процесс obfs4 остановился. Проверьте Lyrebird и перезапустите чат; очередь сохранена."
	}
	var bootstrap *tor.BootstrapError
	if errors.As(err, &bootstrap) {
		p := bootstrap.Progress
		return fmt.Sprintf("Tor не завершил запуск: %s (%d%%). Проверьте сеть и настройки мостов в tor.json; при медленном подключении увеличьте --startup-timeout.", torProgressText(p), max(0, min(100, p.Percent)))
	}
	return ""
}
