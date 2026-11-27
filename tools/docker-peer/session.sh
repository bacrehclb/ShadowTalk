#!/bin/bash
# One private D-Bus session and encrypted login keyring per interactive peer.
set -euo pipefail
umask 077

if [[ ! -t 0 || ! -t 1 ]]; then
    printf 'Run this test peer with docker run -it (or docker start -ai).\n' >&2
    exit 2
fi
mkdir -p "$XDG_RUNTIME_DIR"
chmod 700 "$XDG_RUNTIME_DIR"
if [[ ${1-} != --session ]]; then
    exec dbus-run-session -- "$0" --session
fi

mkdir -p "$XDG_RUNTIME_DIR/keyring" "$XDG_CONFIG_HOME" "$XDG_DATA_HOME/keyrings"
chmod 700 "$XDG_RUNTIME_DIR" "$XDG_RUNTIME_DIR/keyring" "$XDG_DATA_HOME/keyrings"
printf 'Unlock the container keyring. On first use, choose a new non-empty password.\n'
IFS= read -r -s -p 'Keyring password: ' keyring_password
printf '\n'
if [[ -z $keyring_password ]]; then
    printf 'An empty keyring password is not supported by this test setup.\n' >&2
    exit 2
fi
if [[ ! -f "$XDG_DATA_HOME/keyrings/login.keyring" ]]; then
    IFS= read -r -s -p 'Repeat new password: ' confirmation
    printf '\n'
    if [[ $keyring_password != "$confirmation" ]]; then
        printf 'Passwords do not match. Start the container again.\n' >&2
        exit 2
    fi
    unset confirmation
fi

# Password travels only over stdin, never through process arguments or a file.
printf '%s' "$keyring_password" | timeout 15s gnome-keyring-daemon \
    --daemonize --unlock --components=secrets \
    --control-directory="$XDG_RUNTIME_DIR/keyring" >/dev/null
unset keyring_password

# A wrong password may leave the daemon running but the collection locked.
# Fail here instead of allowing a later CLI call to wait for a graphical prompt.
if ! locked=$(timeout 5s gdbus call --session --dest org.freedesktop.secrets \
    --object-path /org/freedesktop/secrets/collection/login \
    --method org.freedesktop.DBus.Properties.Get \
    org.freedesktop.Secret.Collection Locked) || [[ $locked != '(<false>,)' ]]; then
    printf 'The login keyring is still locked. Restart and enter its original password.\n' >&2
    exit 1
fi

printf '\nShadowTalk Linux peer is ready.\n'
shadowtalk --version
printf '\nFirst use: shadowtalk profile create --nick linux_test\n'
printf 'Existing profile: shadowtalk profile show\n'
printf 'Run ShadowTalk commands in this shell so they share the unlocked keyring.\n'
printf 'Stop chat with Esc/Ctrl+C, then type exit to stop this container.\n\n'
export PS1='shadowtalk-linux:\w\$ '
exec /bin/bash --noprofile --norc -i
