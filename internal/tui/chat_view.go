package tui

import (
	"fmt"
	"strings"
	"unicode"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/bacrehclb/ShadowTalk/internal/delivery"
	"github.com/bacrehclb/ShadowTalk/internal/history"
	"github.com/charmbracelet/x/ansi"
)

// Content is sanitized before wrapping or applying our own styles. Newlines
// remain text structure; C0/C1, ESC, bidi/format controls and Unicode line
// separators cannot become terminal commands or overwrite a message header.
func safeMessage(text string) string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\t", "    ")
	return strings.Map(func(r rune) rune {
		if r == '\n' || r == '\u200c' || r == '\u200d' {
			return r
		}
		if unicode.IsControl(r) || unicode.In(r, unicode.Cf) || r == '\u2028' || r == '\u2029' {
			return '�'
		}
		return r
	}, text)
}

func renderEntries(entries []ChatEntry, width int) string {
	var blocks []string
	for i := len(entries) - 1; i >= 0; i-- {
		entry := entries[i]
		sender := "Собеседник"
		status := ""
		if entry.Direction == history.Outgoing {
			sender = "Вы"
			switch entry.State {
			case delivery.Pending:
				status = " · ожидает доставки"
			case delivery.Delivered:
				status = " · доставлено"
			default:
				status = " · сохранено (старое)"
			}
		}
		header := sender + " · " + entry.RecordedAt.UTC().Format("02.01 15:04") + status
		body := ansi.Hardwrap(safeMessage(entry.Text), max(1, width-2), true)
		body = "│ " + strings.ReplaceAll(body, "\n", "\n│ ")
		blocks = append(blocks, mutedStyle.Render(ansi.Hardwrap(header, width, true))+"\n"+body+"\n")
	}
	return strings.Join(blocks, "\n")
}
func historyLines(entries []ChatEntry, width int) int {
	if len(entries) == 0 {
		return 0
	}
	return lipgloss.Height(renderEntries(entries, width)) + 1
}
func (m *chatModel) renderHistory() {
	text := renderEntries(m.entries, m.messageWidth())
	if len(m.entries) == 0 {
		text = mutedStyle.Render("Сообщений пока нет.")
	}
	m.viewport.SetContent(text)
}

func (m chatModel) View() tea.View {
	var content string
	if m.closing {
		content = "SHADOWTALK\n\nЗавершаем операции и останавливаем Tor…"
	} else if m.tooSmallChat() {
		content = "Увеличьте окно до 60 × 20.\nEsc / Ctrl+C — выход"
	} else if m.opening {
		stage := "Загрузка профиля и контактов"
		switch m.stage.Stage {
		case ChatOpeningHistory:
			stage = "Открытие зашифрованной истории"
		case ChatConnectingTor:
			stage = "Подключение Tor и публикация адреса"
			if m.stage.Detail != "" {
				stage = "Подключение Tor: " + safeText(m.stage.Detail)
			}
			if m.stage.HasPercent {
				stage += fmt.Sprintf(" · %d%%", max(0, min(100, m.stage.Percent)))
			}
		}
		content = titleStyle.Render("SHADOWTALK") + "\n\n" + ansi.Hardwrap(stage+fmt.Sprintf(" · %d с", m.seconds), m.width, true) + "\n\n" + "ОС может запросить доступ к ключам.\nПодключение Tor может занять несколько минут.\n\nEsc / Ctrl+C — отменить и выйти"
	} else if m.session == nil {
		content = titleStyle.Render("SHADOWTALK") + "\n\nНе удалось открыть чат\n\n" + ansi.Hardwrap(m.describe(m.failure), m.width, true) + "\n\nEnter / r — повторить · Esc / Ctrl+C — выход"
	} else {
		header := titleStyle.Render("SHADOWTALK") + "  " + safeText(m.session.Profile().Nickname) + mutedStyle.Render(" · Tor готов · время UTC")
		listHeight := max(1, m.height-11)
		leftTitle := "Контакты"
		if m.contactFocus {
			leftTitle = "Контакты ◀"
		}
		left := []string{labelStyle.Render(leftTitle)}
		start := max(0, min(m.selected-listHeight/2, len(m.contacts)-listHeight))
		for i := start; i < min(len(m.contacts), start+listHeight); i++ {
			prefix := "  "
			if m.activity[m.contacts[i].PublicKey] > m.seen[m.contacts[i].PublicKey] {
				prefix = "• "
			}
			if i == m.selected {
				prefix = "› "
			}
			line := prefix + safeText(m.contacts[i].Alias)
			line = ansi.Truncate(line, 22, "…")
			if i == m.selected {
				line = actionStyle.Render(line)
			}
			left = append(left, line)
		}
		rightTitle := "Выберите контакт"
		body := m.viewport.View()
		if len(m.contacts) > 0 {
			rightTitle = safeText(m.contacts[m.selected].Alias)
			if m.loading {
				rightTitle += " · загрузка…"
			}
		} else {
			body = ansi.Hardwrap("Контактов нет. Добавьте собеседника командой contacts add и перезапустите чат. Для начала нужны взаимные приглашения и подтверждение ключей.", m.messageWidth(), true)
		}
		leftPanel := lipgloss.NewStyle().Width(24).Height(listHeight + 1).Render(strings.Join(left, "\n"))
		rightPanel := lipgloss.NewStyle().Width(m.messageWidth()).Height(listHeight + 1).Render(labelStyle.Render(fit(rightTitle, m.messageWidth(), 1)) + "\n" + fit(body, m.messageWidth(), listHeight))
		panels := lipgloss.JoinHorizontal(lipgloss.Top, leftPanel, "│", rightPanel)
		notice := m.notice
		if len(m.contacts) > 0 {
			if d := m.drafts[m.peer()]; d != nil && d.failure != nil {
				notice = m.describe(d.failure) + " · Ctrl+R — повторить тот же ID"
			}
		}
		footer := fmt.Sprintf("Enter: отправить · Alt+Enter: строка · Tab: контакты/ввод · %d/16384 Б", len(m.composer.Value()))
		content = header + "\n" + mutedStyle.Render(strings.Repeat("─", m.width)) + "\n" + panels + "\n" + mutedStyle.Render(strings.Repeat("─", m.width)) + "\n" + m.composer.View() + "\n" + fit(safeText(notice), m.width, 1) + "\n" + fit(footer, m.width, 1) + "\n" + fit("PgUp/PgDn: история · Ctrl+L: последние · Esc / Ctrl+C: выход", m.width, 1)
	}
	view := tea.NewView(fit(content, m.width, m.height))
	view.AltScreen = true
	return view
}
