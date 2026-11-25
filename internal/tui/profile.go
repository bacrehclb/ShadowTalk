// Package tui implements the interactive terminal interface. Profile operations
// run outside the update loop; messages and screen state contain public data only.
package tui

import (
	"errors"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"github.com/bacrehclb/ShadowTalk/internal/app"
	"github.com/bacrehclb/ShadowTalk/internal/identity"
)

// Profiles is the public-data boundary of the UI. The application adapter must
// discard private keys before returning an operation's result to this interface.
type Profiles interface {
	Load() (identity.Profile, error)
	Create(nickname string) (identity.Profile, error)
	Upgrade() (identity.Profile, error)
	Recover() (identity.Profile, error)
}

type screen uint8

const (
	screenCreate screen = iota
	screenReady
	screenLegacy
	screenRecovery
	screenError
)

type operation uint8

const (
	opLoad operation = iota
	opCreate
	opUpgrade
	opRecover
)

type operationDone struct {
	op      operation
	profile identity.Profile
	err     error
}

type exitRequested struct{}

type model struct {
	profiles       Profiles
	directory      string
	describeError  func(error) string
	nickname       textinput.Model
	profile        identity.Profile
	screen         screen
	op             operation
	busy, quitting bool
	width, height  int
	failure        error
	notice         string
	validation     string
}

func newModel(profiles Profiles, options Options, width, height int) model {
	input := textinput.New()
	input.Prompt = "> "
	input.Placeholder = "ivan_01"
	// Let validation explain overlong nicknames instead of silently accepting
	// their first 24 characters. Still bound untrusted pasted input.
	input.CharLimit = 128
	input.SetStyles(inputStyles())
	input.SetWidth(min(28, max(1, width-10)))
	return model{
		profiles: profiles, directory: options.Directory, describeError: options.DescribeError,
		nickname: input, width: width, height: height, busy: true, op: opLoad,
	}
}

func (m model) Init() tea.Cmd { return m.perform(opLoad) }

func (m model) perform(op operation) tea.Cmd {
	profiles, nickname := m.profiles, m.nickname.Value()
	return func() tea.Msg {
		var profile identity.Profile
		var err error
		switch op {
		case opLoad:
			profile, err = profiles.Load()
		case opCreate:
			profile, err = profiles.Create(nickname)
		case opUpgrade:
			profile, err = profiles.Upgrade()
		case opRecover:
			profile, err = profiles.Recover()
		}
		return operationDone{op: op, profile: profile, err: err}
	}
}

func (m model) start(op operation) (tea.Model, tea.Cmd) {
	m.busy, m.op = true, op
	m.failure, m.validation = nil, ""
	m.nickname.Blur()
	return m, m.perform(op)
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.nickname.SetWidth(min(28, max(1, m.width-10)))
		return m, nil
	case exitRequested:
		return m.exit()
	case operationDone:
		if !m.busy || msg.op != m.op {
			return m, nil
		}
		m.busy = false
		if m.quitting {
			// Do not abandon a key-store write in a background goroutine when the
			// terminal closes. Return only after the application operation ends.
			m.failure = msg.err
			if msg.op == opLoad && errors.Is(msg.err, identity.ErrProfileNotFound) {
				m.failure = nil
			}
			return m, tea.Quit
		}
		return m.completed(msg)
	case tea.KeyPressMsg:
		switch msg.String() {
		case "ctrl+c", "esc":
			return m.exit()
		}
		if m.busy || m.tooSmall() {
			return m, nil
		}
		switch msg.String() {
		case "enter":
			m.notice = ""
			switch m.screen {
			case screenCreate:
				if err := identity.ValidateNickname(m.nickname.Value()); err != nil {
					m.validation = "Ник: 3–24 символа a–z, A–Z, 0–9, _ и -; буква или цифра по краям."
					return m, nil
				}
				return m.start(opCreate)
			case screenLegacy:
				return m.start(opUpgrade)
			case screenRecovery:
				return m.start(opRecover)
			default:
				return m.start(opLoad)
			}
		case "r":
			if m.screen != screenCreate {
				m.notice = ""
				return m.start(opLoad)
			}
		}
	}
	if !m.busy && !m.tooSmall() && m.screen == screenCreate {
		var cmd tea.Cmd
		previous := m.nickname.Value()
		m.nickname, cmd = m.nickname.Update(msg)
		if previous != m.nickname.Value() {
			m.validation = ""
		}
		return m, cmd
	}
	return m, nil
}

func (m model) completed(result operationDone) (tea.Model, tea.Cmd) {
	m.failure = result.err
	if result.err == nil {
		m.profile = result.profile
		m.screen = screenReady
		if !m.profile.HasIdentity() {
			m.screen = screenLegacy
		}
		return m, nil
	}
	switch {
	case result.op == opLoad && errors.Is(result.err, identity.ErrProfileNotFound):
		m.failure, m.screen = nil, screenCreate
		return m, m.nickname.Focus()
	case errors.Is(result.err, app.ErrProfilePending):
		m.screen = screenRecovery
	case errors.Is(result.err, app.ErrCreationAborted):
		m.notice = "Незавершённое создание отменено: ключ не был сохранён. Можно повторить создание ключей."
		return m.start(opLoad)
	case errors.Is(result.err, identity.ErrProfileExists), errors.Is(result.err, app.ErrNoPendingProfile):
		// Another process may have finished this operation. Re-read its state;
		// never retry a mutation merely because its first outcome was uncertain.
		return m.start(opLoad)
	default:
		m.screen = screenError
	}
	return m, nil
}

func (m model) exit() (tea.Model, tea.Cmd) {
	if m.busy {
		m.quitting = true
		return m, nil
	}
	return m, tea.Quit
}

func (m model) tooSmall() bool { return m.width < 40 || m.height < 20 }
