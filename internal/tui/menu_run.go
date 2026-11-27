package tui

import (
	"errors"
	"os"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/bacrehclb/ShadowTalk/internal/contacts"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/term"
)

// MenuServices performs local operations. Private keys never enter the model.
type MenuServices interface {
	CreateInvitation(time.Duration) (string, error)
	ImportContact(token, alias, fingerprint string) error
	ListContacts() ([]contacts.Contact, error)
	RemoveContact(alias, fingerprint string) error
	LoadTor() (TorSettings, error)
	SaveTor(TorSettings) error
	SaveInvitation(path, token string) error
	ReadInvitation(path string) (string, error)
}

type TorSettings struct {
	Mode, Bridges, Timeout, TorBinary, LyrebirdBinary string
}

type MenuAction int

const (
	MenuExit MenuAction = iota
	MenuProfile
	MenuChat
)

type MenuResult struct {
	Action  MenuAction
	Contact string
}

type MenuOptions struct {
	Directory     string
	Notice        string
	DescribeError func(error) string
	CopyText      func(string) error // optional native clipboard; otherwise terminal OSC 52
}

// RunMenu returns before opening profile/chat so only one program owns the TTY.
func RunMenu(service MenuServices, input, output *os.File, options MenuOptions) (MenuResult, error) {
	if input == nil || output == nil || !term.IsTerminal(input.Fd()) || !term.IsTerminal(output.Fd()) {
		return MenuResult{}, errors.New("для menu нужны терминальные stdin и stdout; запустите shadowtalk menu в терминале")
	}
	if os.Getenv("TERM") == "dumb" {
		return MenuResult{}, errors.New("терминал TERM=dumb не поддерживает menu")
	}
	w, h, err := term.GetSize(output.Fd())
	if err != nil {
		return MenuResult{}, errors.New("не удалось определить размер терминала")
	}
	opts := []tea.ProgramOption{tea.WithInput(input), tea.WithOutput(output), tea.WithWindowSize(w, h), tea.WithFilter(menuExitFilter)}
	if os.Getenv("NO_COLOR") != "" {
		opts = append(opts, tea.WithColorProfile(colorprofile.ASCII))
	}
	final, err := tea.NewProgram(newMenu(service, options, w, h), opts...).Run()
	if err != nil {
		return MenuResult{}, err
	}
	return final.(*menuModel).result, nil
}

func menuExitFilter(current tea.Model, msg tea.Msg) tea.Msg {
	switch msg.(type) {
	case tea.QuitMsg, tea.InterruptMsg:
		if m, ok := current.(*menuModel); ok && m.busy {
			return exitRequested{}
		}
	}
	return msg
}
