# Ручная проверка macOS ↔ Linux в Docker

Один клиент запускается из установленного `ShadowTalk.app`, второй — из Linux DEB
alpha.2 в контейнере на том же Mac. У каждого свой профиль, keyring, история и
Tor-процесс. Сообщения должны пройти через настоящую сеть Tor и onion-адреса;
порты контейнера публиковать не нужно.

Это проверка взаимодействия macOS/Linux и повторной доставки. Она не заменяет
проверку двух физических устройств в разных сетях. Наличие этого руководства
не означает, что обмен через публичный Tor уже прошёл.

## 1. Собрать образ — в обычном Terminal на Mac

Запустите Docker Desktop. Все команды сборки выполняются из корня репозитория.
Нужен каталог релизных файлов с Linux DEB для архитектуры Docker и его
`checksums.txt`. Для текущей локальной подготовки alpha.2 это `dist/alpha2-input`:

```sh
docker version
docker build --tag shadowtalk-peer:alpha.2 \
  --build-context release=dist/alpha2-input \
  tools/docker-peer
```

Docker автоматически выбирает архитектуру: обычно `arm64` на Apple Silicon и
`amd64` на Intel. Если релизные файлы скачаны в другой каталог, подставьте его
после `release=`. Нужны `shadowtalk_0.1.0-alpha.2_linux_ARCH.deb` и контрольные
суммы из того же релиза. Сборка сверяет SHA-256 пакета перед установкой.

Образ устанавливает готовый релизный DEB вместе с его Tor/Lyrebird, а не
пересобирает Go-код. Из Debian добавляются D-Bus и GNOME Keyring для настоящего
Linux-хранилища ключей. Сборка скачивает базовый образ и пакеты; приватные профили,
приглашения и мосты в образ не копируются.

Проверить установленную версию без запуска keyring:

```sh
docker run --rm --entrypoint shadowtalk shadowtalk-peer:alpha.2 --version
```

Ожидается `0.1.0-alpha.2` и `linux/arm64` либо `linux/amd64`.

## 2. Запустить Linux-клиент — второе окно Terminal

```sh
docker run --init -it --name shadowtalk-peer --stop-timeout 30 \
  --mount type=volume,source=shadowtalk-peer-data,target=/home/peer \
  shadowtalk-peer:alpha.2
```

При первом запуске придумайте отдельный непустой пароль keyring и повторите его.
При последующих запусках нужен тот же пароль. Он вводится скрыто и передаётся
GNOME Keyring через stdin; сохранять его в команде, образе или репозитории не нужно.
Это пароль тестового Linux-хранилища, не пароль macOS.

После `ShadowTalk Linux peer is ready` выполняйте команды **в этом окне контейнера**:

```sh
shadowtalk profile create --nick linux_test
shadowtalk profile show
shadowtalk invite create --ttl 24h > /home/peer/linux.invite
```

`profile create` нужен только один раз. При повторном запуске используйте
`profile show`. Не запускайте второй контейнер одновременно с тем же volume.
Команды ShadowTalk выполняйте в этой оболочке: она наследует адрес приватной
D-Bus-сессии с разблокированным keyring. Обычный `docker exec ... shadowtalk`
эту среду не наследует.

## 3. Создать отдельный профиль Mac — первое окно Terminal

Так тест не добавляет контакты и сообщения в ваш основной профиль. Функция ниже
действует только в текущем окне Terminal и не изменяет конфигурацию shell:

```sh
stmac() {
  "/Applications/ShadowTalk.app/Contents/MacOS/shadowtalk" \
    --profile-dir "$HOME/Library/Application Support/shadowtalk-docker-test/mac" "$@"
}

stmac --version
stmac profile create --nick mac_test
stmac profile show
mkdir -p dist/docker-peer-exchange
stmac invite create --ttl 24h > dist/docker-peer-exchange/mac.invite
```

Mac тоже должен показывать `0.1.0-alpha.2`. Создание профиля может запросить доступ
к Keychain. Для продолжения теста с существующим профилем повторите определение
`stmac`, затем используйте `stmac profile show` вместо создания.

## 4. Передать приглашения и подтвердить ключи

В первом окне **на Mac**, пока контейнер запущен:

```sh
docker exec shadowtalk-peer cat /home/peer/linux.invite \
  > dist/docker-peer-exchange/linux.invite
docker exec -i shadowtalk-peer sh -c 'umask 077; cat > /home/peer/mac.invite' \
  < dist/docker-peer-exchange/mac.invite
stmac invite inspect --file dist/docker-peer-exchange/linux.invite
```

Сравните полный fingerprint приглашения с `shadowtalk profile show` во втором
окне. Вместо `SHA256:...` подставьте полный fingerprint **Linux-профиля**:

```sh
stmac contacts add --alias linux_peer --fingerprint 'SHA256:...' \
  --file dist/docker-peer-exchange/linux.invite
```

Во втором окне **в контейнере**:

```sh
shadowtalk invite inspect --file /home/peer/mac.invite
```

Сравните fingerprint с `stmac profile show` на Mac. Добавьте контакт с полным
fingerprint **Mac-профиля**:

```sh
shadowtalk contacts add --alias mac_peer --fingerprint 'SHA256:...' \
  --file /home/peer/mac.invite
```

Обе стороны должны сохранить друг друга. Передаются только приглашения — не
`identity-seed.json`, профиль, история или файлы keyring.

## 5. Настроить Tor

Если прямой Tor работает в вашей сети, отдельный конфиг не нужен. Если раньше
использовались obfs4-мосты, задайте рабочие мосты для тестовых профилей:

- Mac: `~/Library/Application Support/shadowtalk-docker-test/mac/tor.json`;
- контейнер: `/home/peer/.config/shadowtalk/tor.json`.

Формат — в [руководстве Tor](../bundled-tor.md#using-it). В конфиге достаточно
`version`, `mode`, `bridges` и `startup_timeout`. Не переносите в контейнер
macOS-пути `tor_binary` / `lyrebird_binary`: в alpha.2 комплектные программы
находятся автоматически.

Если оба тестовых клиента используют одинаковые мосты, после сохранения конфига
Mac без явных путей к бинарникам скопируйте **только этот конфиг**:

```sh
chmod 600 "$HOME/Library/Application Support/shadowtalk-docker-test/mac/tor.json"
docker exec -i shadowtalk-peer sh -c \
  'umask 077; cat > /home/peer/.config/shadowtalk/tor.json' \
  < "$HOME/Library/Application Support/shadowtalk-docker-test/mac/tor.json"
```

Не выполняйте этот блок при прямом Tor без файла конфигурации. Мосты хранятся
в профилях, не в Dockerfile или Git. Каждый клиент может использовать свой набор.

## 6. Проверить сообщения и повторную доставку

На Mac:

```sh
stmac chat --contact linux_peer --startup-timeout 5m
```

В контейнере:

```sh
shadowtalk chat --contact mac_peer --startup-timeout 5m
```

Дождитесь открытия чата на обеих сторонах. Напишите с Mac, ответьте из контейнера;
проверьте текст и статус `доставлено` у обоих отправителей. Если редактор не в
фокусе, нажмите Tab; Enter отправляет сообщение.

Затем проверьте очередь:

1. Выйдите из чата контейнера через Esc или Ctrl+C и выполните `exit`.
2. Пока контейнер остановлен, отправьте с Mac ещё одно сообщение. Оно должно
   остаться в ожидании доставки.
3. Во втором окне, теперь снова на Mac, запустите:

   ```sh
   docker start -ai shadowtalk-peer
   ```

4. Введите прежний пароль keyring, выполните `shadowtalk profile show`, убедитесь,
   что fingerprint сохранился, затем снова запустите чат с `mac_peer`.
5. После подключения сообщение должно прийти один раз, а Mac — показать доставку.
   Перезапустите оба чата и убедитесь, что история сохранилась.

Запишите версии клиентов, ОС/архитектуры, режим Tor, результат двустороннего
обмена и проверки очереди. Сообщения об ошибках можно включить в отчёт; приватные
ключи, профили, приглашения и строки мостов — нет.

## Данные и повторный запуск

В volume `shadowtalk-peer-data` находятся одновременно:

- `/home/peer/.config/shadowtalk` — профиль, контакты, история и Tor-конфиг;
- `/home/peer/.local/share/keyrings` — зашифрованное Linux-хранилище ключей.

Остановка контейнера их не удаляет. Для продолжения используйте
`docker start -ai shadowtalk-peer`, а не повторный `docker run` с занятым именем.
Сохраняйте volume целиком: профиль без своего keyring недостаточен для восстановления.
Пароль keyring тоже потребуется после перезапуска.

При `system key store access failed` проверьте, что вошли через стартовую оболочку,
ввели прежний пароль и не потеряли keyring volume. При неверном пароле оболочка
должна завершиться до запуска ShadowTalk. При тайм-ауте Tor проверьте сеть и мосты;
публикация Docker-портов эту проблему не исправляет.

Чтобы полностью удалить **только тестовый Linux-профиль**, сначала штатно
завершите чат и оболочку. Следующие команды необратимо удалят его историю и ключи:

```sh
docker rm shadowtalk-peer
docker volume rm shadowtalk-peer-data
```

Mac-профиль и Keychain эти команды не затрагивают. Сам Docker-образ остаётся
доступным для новой проверки.

## Что уже проверено в этом рецепте

27 ноября 2026 года образ собран и запущен в Docker Desktop на Mac arm64 с
релизным Linux arm64 DEB `0.1.0-alpha.2` (коммит `8ed7af2`). Проверены версия CLI,
запуск комплектных Tor/Lyrebird, создание профиля через настоящий GNOME Keyring,
выпуск приглашения и доступ к тому же профилю/приглашению из нового контейнера
с прежним volume. Неверный пароль завершается ошибкой, запуск без TTY — быстрым
отказом с инструкцией. Тестовые контейнеры и их отдельный volume удалены.

Публичная сеть Tor и обмен с macOS-клиентом в эту проверку рецепта не входили:
их нужно выполнить по шагам 3–6. Контейнер amd64 в этом запуске не проверялся.

## Источники

- [Docker: named build contexts](https://docs.docker.com/build/concepts/context/#named-contexts)
- [Docker: interactive containers](https://docs.docker.com/engine/containers/run/)
- [Docker: persistent volumes](https://docs.docker.com/engine/storage/volumes/)
- [GNOME Keyring: unlock/create the login keyring via stdin](https://manpages.debian.org/trixie/gnome-keyring/gnome-keyring-daemon.1.en.html)
