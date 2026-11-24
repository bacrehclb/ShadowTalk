package tui

import (
	"context"
	"errors"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"github.com/bacrehclb/ShadowTalk/internal/chat"
	"github.com/bacrehclb/ShadowTalk/internal/contacts"
	"github.com/bacrehclb/ShadowTalk/internal/history"
	"github.com/bacrehclb/ShadowTalk/internal/protocol"
)

const maxChatEntries = 200

type chatTick uint64
type chatDraft struct {
	text    string
	pending *chat.Message
	saving  bool
	failure error
}
type chatModel struct {
	clock                    uint64
	activity, seen           map[[32]byte]uint64
	runtime                  *chatRuntime
	options                  ChatOptions
	session                  ChatSession
	contacts                 []contacts.Contact
	selected                 int
	contactFocus             bool
	composer                 textarea.Model
	viewport                 viewport.Model
	entries                  []ChatEntry
	nextBefore               int64
	request                  uint64
	queryCancel              context.CancelFunc
	loading, latest, dirty   bool
	drafts                   map[[32]byte]*chatDraft
	width, height            int
	stage                    ChatStage
	seconds                  int
	opening, closing, closed bool
	failure                  error
	notice                   string
}

func newChatModel(r *chatRuntime, o ChatOptions, width, height int) *chatModel {
	input := textarea.New()
	input.Prompt = "› "
	input.Placeholder = "Сообщение…"
	input.ShowLineNumbers = false
	input.CharLimit = protocol.MaxTextBytes + 1
	input.MinHeight = 3
	input.MaxHeight = 3
	input.MaxContentHeight = protocol.MaxTextBytes
	input.SetVirtualCursor(true)
	input.KeyMap.InsertNewline = key.NewBinding(key.WithKeys("alt+enter", "ctrl+j"))
	input.KeyMap.Paste.SetEnabled(false) // Terminal bracketed paste is handled below.
	m := &chatModel{clock: 1, activity: make(map[[32]byte]uint64), seen: make(map[[32]byte]uint64), runtime: r, options: o, composer: input, viewport: viewport.New(), width: width, height: height, opening: true, latest: true, drafts: make(map[[32]byte]*chatDraft)}
	m.resize()
	return m
}
func (m chatModel) Init() tea.Cmd {
	return tea.Batch(m.runtime.openCmd, m.runtime.stageCmd, chatClock(m.clock))
}
func chatClock(generation uint64) tea.Cmd {
	return tea.Tick(time.Second, func(time.Time) tea.Msg { return chatTick(generation) })
}
func (m *chatModel) resize() {
	m.composer.SetWidth(max(1, m.width))
	m.composer.SetHeight(3)
	m.viewport.SetWidth(m.messageWidth())
	m.viewport.SetHeight(max(1, m.height-11))
	m.renderHistory()
}
func (m chatModel) tooSmallChat() bool { return m.width < 60 || m.height < 20 }
func (m chatModel) messageWidth() int  { return max(1, m.width-25) }
func (m chatModel) peer() [32]byte {
	if len(m.contacts) == 0 {
		return [32]byte{}
	}
	return m.contacts[m.selected].PublicKey
}
func (m *chatModel) draft() *chatDraft {
	p := m.peer()
	if m.drafts[p] == nil {
		m.drafts[p] = &chatDraft{}
	}
	return m.drafts[p]
}
func (m *chatModel) focus() tea.Cmd {
	if m.contactFocus || len(m.contacts) == 0 || m.draft().pending != nil || m.closing {
		m.composer.Blur()
		return nil
	}
	return m.composer.Focus()
}
func (m *chatModel) load(older bool) tea.Cmd {
	if m.session == nil || len(m.contacts) == 0 {
		return nil
	}
	if m.queryCancel != nil {
		m.queryCancel()
	}
	m.request++
	m.loading = true
	q := history.Query{Limit: 50}
	if older {
		q.Before = m.nextBefore
		m.latest = false
	}
	cmd, cancel := m.runtime.history(m.session, m.peer(), q, m.request)
	m.queryCancel = cancel
	return cmd
}
func (m *chatModel) selectContact(index int) tea.Cmd {
	if len(m.contacts) == 0 {
		return nil
	}
	m.selected = (index + len(m.contacts)) % len(m.contacts)
	m.composer.SetValue(m.draft().text)
	m.entries = nil
	m.nextBefore = 0
	m.latest = true
	m.dirty = false
	m.notice = ""
	m.renderHistory()
	return tea.Batch(m.focus(), m.load(false))
}
func (m *chatModel) exit() tea.Cmd {
	if m.closing {
		return nil
	}
	m.closing = true
	m.composer.Blur()
	return m.runtime.closeCmd
}

func (m *chatModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.resize()
		return m, nil
	case exitRequested:
		return m, m.exit()
	case chatClosed:
		m.closed = true
		if msg.err != nil {
			m.failure = errors.Join(m.failure, msg.err)
		}
		return m, tea.Quit
	case chatTick:
		if uint64(msg) != m.clock {
			return m, nil
		}
		if m.opening && !m.closing {
			m.seconds++
			return m, chatClock(m.clock)
		}
		return m, nil
	case ChatStage:
		if m.closing {
			return m, nil
		}
		if m.opening {
			m.stage = msg
		}
		return m, m.runtime.stageCmd
	case chatOpened:
		m.opening = false
		if m.closing {
			return m, nil
		}
		m.failure = msg.err
		if msg.err != nil {
			return m, nil
		}
		m.session = msg.session
		m.contacts = msg.session.Contacts()
		m.activity = msg.session.Activity()
		for i, c := range m.contacts {
			if c.Alias == m.options.InitialContact {
				m.selected = i
			}
		}
		return m, tea.Batch(m.focus(), m.load(false), m.runtime.watch(m.session))
	case chatChanged:
		m.activity = msg.activity
		if m.closing {
			return m, nil
		}
		if msg.stopped {
			m.failure = msg.err
			if m.failure == nil {
				m.failure = errors.New("чат остановлен")
			}
			return m, m.exit()
		}
		var refresh tea.Cmd
		if m.loading {
			m.dirty = true
		} else if m.latest && m.viewport.AtBottom() {
			refresh = m.load(false)
		} else {
			m.notice = "Есть изменения · Ctrl+L — последние сообщения"
		}
		return m, tea.Batch(refresh, m.runtime.watch(m.session))
	case chatHistory:
		if msg.request != m.request || m.closing {
			return m, nil
		}
		m.loading = false
		if msg.err != nil {
			m.notice = m.describe(msg.err)
			return m, nil
		}
		oldOffset := m.viewport.YOffset()
		if msg.older {
			addedLines := historyLines(msg.page.Entries, m.messageWidth())
			m.entries = append(m.entries, msg.page.Entries...)
			if len(m.entries) > maxChatEntries {
				m.entries = m.entries[len(m.entries)-maxChatEntries:]
			}
			m.renderHistory()
			m.viewport.SetYOffset(oldOffset + addedLines)
		} else {
			m.entries = msg.page.Entries
			m.renderHistory()
			m.viewport.GotoBottom()
		}
		m.nextBefore = msg.page.NextBefore
		if m.latest && m.viewport.AtBottom() {
			m.seen[m.peer()] = m.activity[m.peer()]
		}
		if m.dirty {
			m.dirty = false
			if m.latest {
				return m, m.load(false)
			}
			m.notice = "Есть изменения · Ctrl+L — последние сообщения"
		}
		return m, nil
	case chatSent:
		d := m.drafts[msg.peer]
		if d == nil || d.pending == nil || d.pending.ID != msg.message.ID {
			return m, nil
		}
		d.saving = false
		d.failure = msg.err
		if msg.err == nil {
			d.text = ""
			d.pending = nil
		}
		if m.closing || m.peer() != msg.peer {
			return m, nil
		}
		m.composer.SetValue(d.text)
		if msg.err != nil {
			m.notice = "Сохранение не подтверждено · Ctrl+R — повторить тот же ID"
			return m, nil
		}
		m.notice = "Сохранено в очереди"
		m.latest = true
		return m, tea.Batch(m.focus(), m.load(false))
	case tea.KeyPressMsg:
		if msg.String() == "esc" || msg.String() == "ctrl+c" {
			return m, m.exit()
		}
		if m.closing || m.tooSmallChat() {
			return m, nil
		}
		if m.opening {
			return m, nil
		}
		if m.session == nil {
			if msg.String() == "enter" || msg.String() == "r" {
				m.opening = true
				m.failure = nil
				m.seconds = 0
				m.clock++
				m.stage = ChatLoadingProfile
				return m, tea.Batch(m.runtime.openCmd, chatClock(m.clock))
			}
			return m, nil
		}
		switch msg.String() {
		case "tab", "shift+tab":
			m.contactFocus = !m.contactFocus
			return m, m.focus()
		case "ctrl+n":
			return m, m.selectContact(m.selected + 1)
		case "ctrl+p":
			return m, m.selectContact(m.selected - 1)
		case "pgup":
			if m.viewport.AtTop() && m.nextBefore != 0 && !m.loading {
				return m, m.load(true)
			}
			m.viewport.ScrollUp(max(1, m.viewport.Height()-1))
			return m, nil
		case "pgdown":
			m.viewport.ScrollDown(max(1, m.viewport.Height()-1))
			return m, nil
		case "ctrl+l":
			m.latest = true
			m.notice = ""
			return m, m.load(false)
		case "ctrl+r":
			if len(m.contacts) > 0 && m.draft().pending != nil {
				return m, m.submit()
			}
			return m, nil
		case "enter":
			if m.contactFocus {
				m.contactFocus = false
				return m, m.focus()
			}
			return m, m.submit()
		case "up":
			if m.contactFocus {
				return m, m.selectContact(m.selected - 1)
			}
		case "down":
			if m.contactFocus {
				return m, m.selectContact(m.selected + 1)
			}
		}
	case tea.PasteMsg:
		msg.Content = safeMessage(msg.Content)
		if len(msg.Content)+len(m.composer.Value()) > protocol.MaxTextBytes {
			m.notice = "Вставка не добавлена: максимум 16384 байта UTF-8."
			return m, nil
		}
		return m, m.edit(msg)
	}
	return m, m.edit(msg)
}

func (m *chatModel) edit(msg tea.Msg) tea.Cmd {
	if m.session == nil || m.closing || m.tooSmallChat() || m.contactFocus || len(m.contacts) == 0 || m.draft().pending != nil {
		return nil
	}
	previous := m.composer.Value()
	var cmd tea.Cmd
	m.composer, cmd = m.composer.Update(msg)
	value := safeMessage(m.composer.Value())
	if len(value) > protocol.MaxTextBytes {
		m.composer.SetValue(previous)
		m.notice = "Максимум 16384 байта UTF-8."
	} else {
		if value != m.composer.Value() {
			m.composer.SetValue(value)
		}
		m.draft().text = value
	}
	return cmd
}
func (m *chatModel) submit() tea.Cmd {
	if len(m.contacts) == 0 {
		return nil
	}
	d := m.draft()
	if d.saving {
		return nil
	}
	if d.pending == nil {
		if strings.TrimSpace(d.text) == "" {
			m.notice = "Введите сообщение."
			return nil
		}
		message, err := chat.NewMessage(d.text)
		if err != nil {
			m.notice = "Сообщение должно содержать не больше 16384 байт UTF-8."
			return nil
		}
		d.pending = &message
	}
	d.saving = true
	d.failure = nil
	m.composer.Blur()
	m.notice = "Сохраняем сообщение…"
	return m.runtime.send(m.session, m.peer(), *d.pending)
}
func (m chatModel) describe(err error) string {
	if m.options.DescribeError != nil {
		return safeText(m.options.DescribeError(err))
	}
	return "Не удалось выполнить операцию. Проверьте сеть и локальные данные."
}
