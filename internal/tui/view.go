package tui

import (
	"fmt"
	"strings"
	"unicode"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/bacrehclb/ShadowTalk/internal/app"
	"github.com/charmbracelet/x/ansi"
)

var (
	titleStyle  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("6"))
	labelStyle  = lipgloss.NewStyle().Bold(true)
	mutedStyle  = lipgloss.NewStyle().Faint(true)
	actionStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("6"))
	errorStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
)

func inputStyles() textinput.Styles {
	return textinput.Styles{
		Focused: textinput.StyleState{Prompt: actionStyle, Placeholder: mutedStyle},
		Blurred: textinput.StyleState{Placeholder: mutedStyle},
		Cursor:  textinput.CursorStyle{Blink: true},
	}
}

// safeText prevents paths and diagnostics from becoming terminal control
// sequences. Layout introduces its own newlines after sanitizing external text.
func safeText(text string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || unicode.In(r, unicode.Cf) {
			return '�'
		}
		return r
	}, text)
}

func (m model) View() tea.View {
	if m.tooSmall() {
		content := "Увеличьте окно до 40 × 20.\nEsc / Ctrl+C — выход"
		if m.quitting {
			content = "Ожидаем завершения операции…"
		}
		view := tea.NewView(fit(content, m.width, m.height))
		view.AltScreen = true
		return view
	}
	width := min(72, m.width-8)
	heading, body, action := m.content(width)
	header := []string{
		titleStyle.Render("SHADOWTALK") + mutedStyle.Render("  /  локальный профиль"),
		mutedStyle.Render(strings.Repeat("─", width)),
		labelStyle.Render(heading),
		"",
	}
	var notices []string
	if m.notice != "" {
		notices = append(notices, fit(ansi.Hardwrap(safeText(m.notice), width, true), width, 3), "")
	}
	if m.validation != "" {
		notices = append(notices, errorStyle.Render(ansi.Hardwrap(m.validation, width, true)), "")
	}
	footer := []string{actionStyle.Render(ansi.Hardwrap(action, width, true)), "", mutedStyle.Render("Esc / Ctrl+C — выход")}
	// Keep the directory to one line; the full path is already supplied by the
	// user or documented default and is not needed to operate the form.
	footer = append(footer, mutedStyle.Render("Каталог: "+ansi.Truncate(safeText(m.directory), width-9, "…")))
	tail := strings.Join(append(notices, footer...), "\n")
	// Reserve the action and exit hints before fitting the body. Long errors
	// may be shortened, but resizing never removes keyboard navigation.
	bodyHeight := m.height - 2 - len(header) - lipgloss.Height(tail) - 1
	panel := strings.Join(header, "\n") + "\n" + fit(body, width, bodyHeight) + "\n\n" + tail
	view := tea.NewView(lipgloss.NewStyle().Margin(1, 4).Render(panel))
	view.AltScreen = true
	return view
}

func (m model) content(width int) (heading, body, action string) {
	wrap := func(text string) string { return ansi.Hardwrap(safeText(text), width, true) }
	if m.busy {
		heading = "Загрузка профиля"
		switch m.op {
		case opCreate:
			heading = "Создание профиля"
		case opUpgrade:
			heading = "Создание ключей"
		case opRecover:
			heading = "Восстановление профиля"
		}
		body = wrap("ОС может запросить доступ к хранилищу ключей.")
		action = "Подождите…"
		if m.quitting {
			action = "Выйдем после завершения операции."
		}
		return
	}
	switch m.screen {
	case screenCreate:
		heading = "Добро пожаловать"
		body = wrap("Придумайте ник. Его увидят ваши собеседники.") + "\n\n" + m.nickname.View() + "\n\n" +
			mutedStyle.Render(wrap("3–24 символа: латиница, цифры, _ и -. По краям — буква или цифра."))
		action = "Enter — создать профиль"
	case screenReady:
		heading = "Профиль готов"
		body = "Ник: " + labelStyle.Render(safeText(m.profile.Nickname)) + "\n" +
			"Создан: " + m.profile.CreatedAt.Format("02.01.2006 15:04 UTC") + "\n\n" +
			mutedStyle.Render("Отпечаток публичного ключа") + "\n" + wrap(m.profile.Fingerprint())
		action = "Enter / r — проверить снова"
	case screenLegacy:
		heading = "Добавьте ключи профиля"
		body = "Ник: " + labelStyle.Render(safeText(m.profile.Nickname)) + "\n\n" +
			wrap("У этого профиля ещё нет ключей. Создайте их, чтобы получить постоянную идентичность. Ник и дата создания сохранятся.")
		action = "Enter — создать ключи"
	case screenRecovery:
		heading = "Создание было прервано"
		body = wrap("Продолжите создание с сохранёнными ключами.")
		if m.failure != app.ErrProfilePending {
			body += "\n\n" + errorStyle.Render(wrap(m.errorText()))
		}
		action = "Enter — восстановить · r — перечитать"
	case screenError:
		heading = "Профиль недоступен"
		body = errorStyle.Render(wrap(m.errorText()))
		action = "Enter / r — проверить снова"
	}
	return
}

func (m model) errorText() string {
	if m.describeError != nil {
		return m.describeError(m.failure)
	}
	return "Не удалось загрузить профиль. Проверьте файлы и доступ к хранилищу ключей."
}

func fit(content string, width, height int) string {
	if width < 1 || height < 1 {
		return ""
	}
	lines := strings.Split(content, "\n")
	if len(lines) > height {
		lines = lines[:height]
		lines[height-1] = fmt.Sprintf("%s…", ansi.Truncate(lines[height-1], max(0, width-1), ""))
	}
	for i := range lines {
		lines[i] = ansi.Truncate(lines[i], width, "…")
	}
	return strings.Join(lines, "\n")
}
