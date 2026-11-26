# Loaded only by the app's private interactive shell; no shell files are edited.
shadowtalk() {
    "$SHADOWTALK_APP_BINARY" "$@"
}
PS1='ShadowTalk \w \$ '
printf '\nКоманды ShadowTalk доступны в этом окне:\n\n'
printf '  shadowtalk --help\n  shadowtalk profile show\n  shadowtalk invite --help\n  shadowtalk contacts --help\n  shadowtalk chat\n\n'
printf 'Для возврата в меню: exit или Ctrl+D.\n'
