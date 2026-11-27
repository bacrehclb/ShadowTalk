package tui

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/textinput"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"github.com/bacrehclb/ShadowTalk/internal/contacts"
	"github.com/bacrehclb/ShadowTalk/internal/identity"
)

type menuScreen int

const (
	menuHome menuScreen = iota
	menuTTL
	menuInvitation
	menuImport
	menuConfirm
	menuContacts
	menuRemove
	menuTor
	menuBridges
	menuTorForm
	menuExport
	menuBridgeHelp
)

type menuDone struct {
	err   error
	apply func(*menuModel) tea.Cmd
}

type menuModel struct {
	service                        MenuServices
	options                        MenuOptions
	screen                         menuScreen
	width, height, selected, focus int
	fields                         []textinput.Model
	labels                         []string
	bridges                        textarea.Model
	preview                        viewport.Model
	token                          string
	invite                         contacts.Invitation
	entries                        []contacts.Contact
	tor                            TorSettings
	busy, quitting                 bool
	notice, problem                string
	result                         MenuResult
}

func newMenu(service MenuServices, options MenuOptions, width, height int) *menuModel {
	b := textarea.New()
	b.CharLimit = 9000
	b.MaxHeight = 0
	b.ShowLineNumbers = false
	b.Prompt = ""
	b.Placeholder = "obfs4 … cert=… iat-mode=0"
	m := &menuModel{service: service, options: options, width: width, height: height, notice: options.Notice, bridges: b, preview: viewport.New()}
	m.resize()
	return m
}

func (m *menuModel) Init() tea.Cmd { return nil }

func (m *menuModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.resize()
		return m, nil
	case exitRequested:
		m.quitting = true
		if !m.busy {
			return m, tea.Quit
		}
		return m, nil
	case menuDone:
		m.busy = false
		var cmd tea.Cmd
		if msg.err != nil {
			m.problem = "Не удалось выполнить операцию."
			if m.options.DescribeError != nil {
				m.problem = m.options.DescribeError(msg.err)
			}
		} else if msg.apply != nil {
			cmd = msg.apply(m)
		}
		if m.quitting {
			return m, tea.Quit
		}
		return m, cmd
	case tea.KeyPressMsg:
		if msg.String() == "ctrl+c" {
			m.quitting = true
			if !m.busy {
				return m, tea.Quit
			}
			return m, nil
		}
		if m.busy {
			return m, nil
		}
		if msg.String() == "esc" {
			if m.screen == menuHome {
				return m, tea.Quit
			}
			if m.screen == menuTorForm && m.tor.Mode == "obfs4" {
				m.tor.Timeout = m.fields[0].Value()
				m.tor.TorBinary = m.fields[1].Value()
				m.tor.LyrebirdBinary = m.fields[2].Value()
				m.fields = nil
				m.screen = menuBridges
				m.problem = ""
				return m, m.bridges.Focus()
			}
			if m.screen == menuExport {
				m.fields = nil
				m.screen = menuInvitation
				m.problem = ""
				return m, nil
			}
			m.home("")
			return m, nil
		}
	}
	if m.busy || m.width < 60 || m.height < 20 {
		return m, nil
	}
	if key, ok := msg.(tea.KeyPressMsg); ok {
		if cmd, handled := m.key(key.String()); handled {
			return m, cmd
		}
	}
	if paste, ok := msg.(tea.PasteMsg); ok {
		if m.screen == menuBridges && utf8.RuneCountInString(paste.Content)+m.bridges.Length() > m.bridges.CharLimit {
			m.problem = "Слишком большой список мостов; вставка отклонена."
			return m, nil
		}
		if len(m.fields) > 0 {
			value := strings.TrimSpace(paste.Content)
			if m.screen == menuImport && m.focus == 0 {
				value = strings.Join(strings.Fields(value), "")
			}
			if utf8.RuneCountInString(value)+utf8.RuneCountInString(m.fields[m.focus].Value()) > m.fields[m.focus].CharLimit {
				m.problem = "Слишком длинное значение; вставка отклонена."
				return m, nil
			}
			msg = tea.PasteMsg{Content: value}
		}
	}
	var cmd tea.Cmd
	if m.screen == menuBridges {
		m.bridges, cmd = m.bridges.Update(msg)
	} else if len(m.fields) > 0 {
		m.fields[m.focus], cmd = m.fields[m.focus].Update(msg)
	} else if m.screen == menuInvitation || m.screen == menuBridgeHelp {
		m.preview, cmd = m.preview.Update(msg)
	}
	return m, cmd
}

func (m *menuModel) key(key string) (tea.Cmd, bool) {
	if len(m.fields) > 0 {
		switch key {
		case "tab", "shift+tab":
			step := 1
			if key == "shift+tab" {
				step = -1
			}
			return m.focusField((m.focus + step + len(m.fields)) % len(m.fields)), true
		case "enter":
			if m.focus+1 < len(m.fields) {
				return m.focusField(m.focus + 1), true
			}
			return m.submit(), true
		case "ctrl+s":
			return m.submit(), true
		}
		return nil, false
	}
	if m.screen == menuBridges {
		if key == "ctrl+s" {
			m.tor.Bridges = m.bridges.Value()
			return m.torForm(), true
		}
		return nil, false
	}
	if m.screen == menuInvitation {
		switch key {
		case "c":
			return m.copyText(m.token), true
		case "f":
			return m.copyText(m.invite.Fingerprint()), true
		case "s":
			return m.form(menuExport, []string{"Сохранить в новый файл"}, []string{filepath.Join(m.options.Directory, "contact.invite")}), true
		}
		return nil, false
	}
	if m.screen == menuRemove {
		if key == "enter" {
			contact := m.entries[m.selected]
			return m.start(func() menuDone {
				return menuDone{err: m.service.RemoveContact(contact.Alias, contact.Fingerprint()), apply: func(m *menuModel) tea.Cmd {
					m.home("Контакт удалён. История не удалена.")
					return nil
				}}
			}), true
		}
		return nil, false
	}
	count := 0
	switch m.screen {
	case menuHome:
		count = 7
	case menuTor:
		count = 3
	case menuContacts:
		count = len(m.entries)
	}
	if count == 0 {
		return nil, false
	}
	switch key {
	case "up":
		m.selected = (m.selected + count - 1) % count
		return nil, true
	case "down":
		m.selected = (m.selected + 1) % count
		return nil, true
	case "enter":
		return m.choose(), true
	case "d":
		if m.screen == menuContacts {
			m.screen = menuRemove
			m.problem = ""
			return nil, true
		}
	}
	return nil, false
}

func (m *menuModel) choose() tea.Cmd {
	m.problem, m.notice = "", ""
	switch m.screen {
	case menuHome:
		switch m.selected {
		case 0:
			m.result.Action = MenuProfile
			return tea.Quit
		case 1:
			m.result.Action = MenuChat
			return tea.Quit
		case 2:
			return m.form(menuTTL, []string{"Срок приглашения (1s–168h)"}, []string{"24h"})
		case 3:
			return m.form(menuImport, []string{"Токен shadowtalk://invite/v1/…", "Или путь файла приглашения (тогда токен пустой)"}, []string{"", ""})
		case 4:
			return m.start(func() menuDone {
				entries, err := m.service.ListContacts()
				return menuDone{err: err, apply: func(m *menuModel) tea.Cmd { m.entries = entries; m.screen = menuContacts; m.selected = 0; return nil }}
			})
		case 5:
			return m.start(func() menuDone {
				settings, err := m.service.LoadTor()
				return menuDone{err: err, apply: func(m *menuModel) tea.Cmd { m.tor = settings; m.screen = menuTor; m.selected = 0; return nil }}
			})
		case 6:
			return tea.Quit
		}
	case menuContacts:
		m.result = MenuResult{MenuChat, m.entries[m.selected].Alias}
		return tea.Quit
	case menuTor:
		switch m.selected {
		case 0:
			m.tor.Mode, m.tor.Bridges, m.tor.LyrebirdBinary = "direct", "", ""
			return m.torForm()
		case 1:
			m.tor.Mode = "obfs4"
			m.screen = menuBridges
			m.bridges.SetValue(m.tor.Bridges)
			return m.bridges.Focus()
		case 2:
			m.screen = menuBridgeHelp
			m.resize()
		}
	}
	return nil
}

func (m *menuModel) submit() tea.Cmd {
	m.problem, m.notice = "", ""
	value := func(i int) string { return strings.TrimSpace(m.fields[i].Value()) }
	switch m.screen {
	case menuTTL:
		ttl, err := time.ParseDuration(value(0))
		if err != nil || contacts.ValidateLifetime(ttl) != nil {
			m.problem = "Срок: от 1s до 168h, целые секунды; например 24h."
			return nil
		}
		return m.start(func() menuDone {
			token, err := m.service.CreateInvitation(ttl)
			return menuDone{err: err, apply: func(m *menuModel) tea.Cmd {
				invite, err := contacts.ParseInvitation(token, time.Now())
				if err != nil {
					m.problem = "Приглашение уже истекло; создайте новое с большим сроком."
					return nil
				}
				m.token, m.invite, m.screen = token, invite, menuInvitation
				m.fields = nil
				m.resize()
				m.preview.GotoTop()
				return nil
			}}
		})
	case menuImport:
		token := strings.Join(strings.Fields(value(0)), "")
		path := value(1)
		if (token == "") == (path == "") {
			m.problem = "Вставьте токен или укажите файл; заполните только одно поле."
			return nil
		}
		if path != "" {
			return m.start(func() menuDone {
				token, err := m.service.ReadInvitation(path)
				return menuDone{err: err, apply: func(m *menuModel) tea.Cmd { return m.inspect(token) }}
			})
		}
		return m.inspect(token)
	case menuConfirm:
		token, alias, fingerprint := m.token, value(0), value(1)
		if identity.ValidateNickname(alias) != nil {
			m.problem = "Имя: 3–24 символа, латиница, цифры, _ или -."
			return nil
		}
		if fingerprint != m.invite.Fingerprint() {
			m.problem = "Отпечаток не совпадает. Получите полный отпечаток у собеседника по доверенному каналу."
			return nil
		}
		return m.start(func() menuDone {
			return menuDone{err: m.service.ImportContact(token, alias, fingerprint), apply: func(m *menuModel) tea.Cmd {
				m.home("Контакт сохранён. Передайте ему своё приглашение тоже, затем оба откройте чат.")
				return nil
			}}
		})
	case menuExport:
		path, token := value(0), m.token
		if path == "" {
			m.problem = "Укажите путь нового файла."
			return nil
		}
		return m.start(func() menuDone {
			return menuDone{err: m.service.SaveInvitation(path, token), apply: func(m *menuModel) tea.Cmd {
				m.fields = nil
				m.screen = menuInvitation
				m.notice = "Приглашение сохранено: " + path
				m.resize()
				return nil
			}}
		})
	case menuTorForm:
		next := m.tor
		next.Timeout, next.TorBinary = value(0), value(1)
		if next.Mode == "obfs4" {
			next.LyrebirdBinary = value(2)
		}
		return m.start(func() menuDone {
			return menuDone{err: m.service.SaveTor(next), apply: func(m *menuModel) tea.Cmd {
				m.home("Настройки Tor сохранены. Они применятся при следующем открытии чата.")
				return nil
			}}
		})
	}
	return nil
}

func (m *menuModel) inspect(token string) tea.Cmd {
	invite, err := contacts.ParseInvitation(token, time.Now())
	if err != nil {
		m.problem = "Приглашение повреждено или истекло. Нужен полный токен, не SHA256-отпечаток."
		return nil
	}
	m.token, m.invite = token, invite
	return m.form(menuConfirm, []string{"Локальное имя контакта", "Отпечаток от собеседника (SHA256:…)"}, []string{invite.Nickname, ""})
}

func (m *menuModel) copyText(text string) tea.Cmd {
	if m.options.CopyText != nil {
		copyText := m.options.CopyText
		return m.start(func() menuDone {
			return menuDone{err: copyText(text), apply: func(m *menuModel) tea.Cmd {
				m.notice = "Скопировано в буфер обмена."
				return nil
			}}
		})
	}
	m.notice = "Запрос копирования отправлен терминалу. Если он не поддерживает OSC 52, нажмите s."
	return tea.SetClipboard(text)
}

func (m *menuModel) start(work func() menuDone) tea.Cmd {
	m.busy, m.problem = true, ""
	return func() tea.Msg { return work() }
}

func (m *menuModel) home(notice string) {
	m.screen, m.selected, m.focus = menuHome, 0, 0
	m.fields, m.labels, m.entries = nil, nil, nil
	m.token, m.invite = "", contacts.Invitation{}
	m.tor = TorSettings{}
	m.bridges.Reset()
	m.bridges.Blur()
	m.preview.SetContent("")
	m.problem, m.notice = "", notice
}

func (m *menuModel) form(screen menuScreen, labels, values []string) tea.Cmd {
	m.screen, m.labels, m.focus = screen, labels, 0
	m.fields = make([]textinput.Model, len(labels))
	m.problem = ""
	for i := range labels {
		f := textinput.New()
		f.Prompt = "> "
		f.CharLimit = 4096
		f.SetStyles(inputStyles())
		if screen == menuImport {
			f.CharLimit = contacts.MaxTokenSize
		}
		f.SetValue(values[i])
		m.fields[i] = f
	}
	m.resize()
	return m.focusField(0)
}

func (m *menuModel) focusField(index int) tea.Cmd {
	for i := range m.fields {
		m.fields[i].Blur()
	}
	m.focus = index
	return m.fields[index].Focus()
}

func (m *menuModel) torForm() tea.Cmd {
	m.bridges.Blur()
	labels := []string{"Ожидание Tor (1s–15m)", "Путь Tor (пусто — автоматически)"}
	values := []string{m.tor.Timeout, m.tor.TorBinary}
	if m.tor.Mode == "obfs4" {
		labels = append(labels, "Путь Lyrebird (пусто — автоматически)")
		values = append(values, m.tor.LyrebirdBinary)
	}
	return m.form(menuTorForm, labels, values)
}

func (m *menuModel) resize() {
	w := max(10, m.width-8)
	for i := range m.fields {
		m.fields[i].SetWidth(w - 4)
	}
	m.bridges.SetWidth(w)
	m.bridges.SetHeight(max(3, m.height-13))
	m.preview.SetWidth(w)
	m.preview.SetHeight(max(3, m.height-11))
	if m.screen == menuInvitation {
		m.preview.SetContent(m.invitationText(w))
	}
	if m.screen == menuBridgeHelp {
		m.preview.SetContent(m.bridgeHelp(w))
	}
}

func (m *menuModel) selectedContact() string {
	c := m.entries[m.selected]
	return fmt.Sprintf("%s (%s)\n%s\n%s", c.Alias, c.Nickname, c.Fingerprint(), c.Onion)
}
