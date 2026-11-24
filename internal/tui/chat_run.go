package tui

import (
	"errors"
	"os"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/term"
)

func RunChat(open OpenChat, input, output *os.File, options ChatOptions) error {
	if input == nil || output == nil || !term.IsTerminal(input.Fd()) || !term.IsTerminal(output.Fd()) {
		return errors.New("для chat нужны терминальные stdin и stdout; запустите shadowtalk chat в терминале")
	}
	if os.Getenv("TERM") == "dumb" {
		return errors.New("терминал TERM=dumb не поддерживает chat")
	}
	width, height, err := term.GetSize(output.Fd())
	if err != nil {
		return errors.New("не удалось определить размер терминала")
	}
	if open == nil {
		return errors.New("не настроен запуск чата")
	}
	runtime := newChatRuntime(open)
	model := newChatModel(runtime, options, width, height)
	programOptions := []tea.ProgramOption{tea.WithInput(input), tea.WithOutput(output), tea.WithWindowSize(width, height), tea.WithFilter(chatExitFilter)}
	if os.Getenv("NO_COLOR") != "" {
		programOptions = append(programOptions, tea.WithColorProfile(colorprofile.ASCII))
	}
	_, runErr := tea.NewProgram(model, programOptions...).Run()
	closeErr := runtime.Close() // Also covers renderer failure and early framework exit.
	if err := errors.Join(model.failure, runErr, closeErr); err != nil {
		return errors.New(model.describe(err))
	}
	return nil
}
func chatExitFilter(current tea.Model, msg tea.Msg) tea.Msg {
	switch msg.(type) {
	case tea.QuitMsg, tea.InterruptMsg:
		if m, ok := current.(*chatModel); ok && !m.closed {
			return exitRequested{}
		}
	}
	return msg
}
