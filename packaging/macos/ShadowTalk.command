#!/bin/bash
# Resolve the bundled binary after copying/renaming the app, including from a DMG.
set -u
if [[ ! -t 0 || ! -t 1 ]]; then
    printf 'ShadowTalk: откройте приложение в Terminal.\n' >&2
    exit 1
fi
resources="$(CDPATH= cd -- "$(dirname -- "$0")" && pwd -P)" || exit 1
export SHADOWTALK_APP_BINARY="${resources%/Resources}/MacOS/shadowtalk"
if [[ ! -x "$SHADOWTALK_APP_BINARY" ]]; then
    printf 'ShadowTalk: исполняемый файл внутри приложения отсутствует.\n' >&2
    exit 1
fi
# Invitations created from the command prompt belong in the user's home, not
# inside a possibly read-only app bundle. Profiles still use the normal OS path.
cd "$HOME" || exit 1

if "$SHADOWTALK_APP_BINARY" menu --help >/dev/null 2>&1; then
    exec "$SHADOWTALK_APP_BINARY" menu
fi

# Compatibility when wrapping the unchanged alpha.1/alpha.2 payload.
while true; do
    printf '\nShadowTalk\n\n  1  Профиль\n  2  Чат\n  3  Команды\n  0  Выход\n\nВыберите действие [1]: '
    IFS= read -r choice || exit 0
    case "$choice" in
        1|'') "$SHADOWTALK_APP_BINARY" ui ;;
        2) "$SHADOWTALK_APP_BINARY" chat ;;
        3) /bin/bash --noprofile --rcfile "$resources/terminal.bash" -i ;;
        0) exit 0 ;;
        *) continue ;;
    esac
    result=$?
    if [[ $result -ne 0 ]]; then
        printf '\nКоманда завершилась с кодом %s.\n' "$result"
    fi
done
