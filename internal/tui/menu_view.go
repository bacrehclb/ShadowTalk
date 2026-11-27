package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

func (m *menuModel) View() tea.View {
	if m.width < 60 || m.height < 20 {
		v := tea.NewView(fit("Увеличьте окно до 60 × 20.\nCtrl+C — выход", m.width, m.height))
		v.AltScreen = true
		return v
	}
	w := m.width - 8
	heading, body, hint := m.content()
	if m.busy {
		body = "Выполняется операция…"
		if m.screen == menuTTL {
			body += "\nОС может запросить доступ к ключам."
		}
	}
	if m.quitting {
		body = "Завершаем текущую операцию перед выходом…"
	}
	tail := ""
	if m.problem != "" {
		tail += errorStyle.Render(fit(ansi.Hardwrap(safeText(m.problem), w, true), w, 3)) + "\n"
	}
	if m.notice != "" {
		tail += fit(ansi.Hardwrap(safeText(m.notice), w, true), w, 2) + "\n"
	}
	tail += actionStyle.Render(fit(hint, w, 2)) + "\n" + mutedStyle.Render("Esc — назад · Ctrl+C — выход")
	header := titleStyle.Render("SHADOWTALK") + mutedStyle.Render("  /  "+heading) + "\n\n"
	bodyHeight := m.height - 5 - lipgloss.Height(tail)
	if !m.busy && !m.quitting && (m.screen == menuInvitation || m.screen == menuBridgeHelp) {
		m.preview.SetHeight(max(1, bodyHeight))
		body = m.preview.View()
	}
	v := tea.NewView(lipgloss.NewStyle().Margin(1, 4).Render(header + fit(body, w, bodyHeight) + "\n\n" + tail))
	v.AltScreen = true
	return v
}

func (m *menuModel) content() (string, string, string) {
	w := m.width - 8
	wrap := func(s string) string { return ansi.Hardwrap(safeText(s), w, true) }
	if len(m.fields) > 0 {
		body, title := "", "параметры"
		switch m.screen {
		case menuTTL:
			title = "создать приглашение"
		case menuImport:
			title = "добавить контакт"
		case menuConfirm:
			title = "проверка контакта"
			body = wrap("Ник: "+m.invite.Nickname) + "\n" + wrap(m.invite.Fingerprint()) + "\n" +
				"Сверьте отпечаток по доверенному каналу.\n\n"
		case menuExport:
			title = "сохранить приглашение"
		case menuTorForm:
			title = "Tor / " + m.tor.Mode
		}
		for i, f := range m.fields {
			body += labelStyle.Render(m.labels[i]) + "\n" + f.View() + "\n"
		}
		return title, strings.TrimSuffix(body, "\n"), "Tab — поле · Enter — далее / сохранить\nCtrl+S — сохранить"
	}
	switch m.screen {
	case menuHome:
		items := []string{"Профиль — создать / проверить", "Открыть чат", "Создать приглашение", "Добавить контакт из приглашения", "Контакты — чат / удаление", "Настройки Tor и мосты", "Выход"}
		return "главное меню", m.choices(items) + "\n\n" + mutedStyle.Render(ansi.Truncate("Профиль: "+safeText(m.options.Directory), w, "…")), "↑ / ↓ — выбор · Enter — открыть"
	case menuInvitation:
		return "ваше приглашение", m.preview.View(), "c — копировать токен · f — отпечаток · s — файл\n↑ / ↓ / PgDn — прокрутка"
	case menuContacts:
		if len(m.entries) == 0 {
			return "контакты", "Контактов пока нет.\nДобавьте приглашение собеседника через главное меню.", "Esc — главное меню"
		}
		var items []string
		// Keep the selected contact visible even for large address books.
		rows := max(1, m.height-15)
		start := max(0, m.selected-rows+1)
		for i := start; i < min(len(m.entries), start+rows); i++ {
			prefix := "  "
			if i == m.selected {
				prefix = "> "
			}
			items = append(items, prefix+safeText(m.entries[i].Alias))
		}
		return "контакты", strings.Join(items, "\n") + fmt.Sprintf("\n\n%d / %d\n", m.selected+1, len(m.entries)) + wrap(m.selectedContact()), "↑ / ↓ — выбор · Enter — чат · d — удалить"
	case menuRemove:
		return "удалить контакт?", wrap(m.selectedContact()) + "\n\nУдалится только запись контакта.\nИстория останется; приглашения не отзываются.", "Enter — подтвердить удаление · Esc — отмена"
	case menuTor:
		return "настройки Tor", "Сейчас: " + safeText(m.tor.Mode) + " · ожидание " + safeText(m.tor.Timeout) + "\n\n" + m.choices([]string{"Прямое подключение", "obfs4 — вставить / изменить мосты", "Где получить мосты"}), "↑ / ↓ — выбор · Enter — настроить"
	case menuBridges:
		return "мосты obfs4", "Вставьте до 16 строк, по одному мосту на строку.\nСписок заменит прежние мосты только после сохранения.\n\n" + m.bridges.View(), "Ctrl+S — далее · Enter — новая строка"
	case menuBridgeHelp:
		return "получить мосты", m.preview.View(), "↑ / ↓ / PgDn — прокрутка · Esc — главное меню"
	}
	return "", "", ""
}

func (m *menuModel) choices(items []string) string {
	var lines []string
	for i, label := range items {
		if i == m.selected {
			lines = append(lines, actionStyle.Render("> "+label))
		} else {
			lines = append(lines, "  "+label)
		}
	}
	return strings.Join(lines, "\n")
}

func (m *menuModel) invitationText(width int) string {
	return ansi.Hardwrap("Ник: "+m.invite.Nickname+"\nОтпечаток: "+m.invite.Fingerprint()+
		"\nДействует до: "+m.invite.ExpiresAt.Local().Format("02.01.2006 15:04:05 MST")+
		"\n\nПередайте весь токен собеседнику:\n\n"+m.token+
		"\n\nОтпечаток SHA256 — для проверки, это не приглашение. Для чата добавьте друг друга в контакты.", width, true)
}

func (m *menuModel) bridgeHelp(width int) string {
	return ansi.Hardwrap("Мосты выдаёт Tor Project.\n\nСайт: https://bridges.torproject.org/\nTelegram: https://t.me/GetBridgesBot\nВ боте отправьте /start, затем /obfs4.\n\nСкопируйте строки полностью, затем выберите «obfs4» в настройках Tor и вставьте список. ShadowTalk проверит формат и сохранит tor.json.\n\nПолучение новых мостов пока не автоматизировано. Проверка формата не гарантирует доступность моста. Если соединение не проходит, запросите новые.\n\nМосты хранятся только в вашем профиле; не публикуйте их в репозитории.", width, true)
}
