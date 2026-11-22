package tui

import (
	"errors"
	"os"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/term"
)

type Options struct {
	Directory     string
	DescribeError func(error) string
}

// Run requires both standard streams to be attached to a capable terminal.
// It never opens /dev/tty to bypass a redirected input or output stream.
func Run(profiles Profiles, input, output *os.File, options Options) error {
	if input == nil || output == nil || !term.IsTerminal(input.Fd()) || !term.IsTerminal(output.Fd()) {
		return errors.New("для ui нужны терминальные stdin и stdout; используйте обычные команды profile или запустите shadowtalk ui в терминале")
	}
	if os.Getenv("TERM") == "dumb" {
		return errors.New("терминал TERM=dumb не поддерживает ui; используйте команды profile")
	}
	width, height, err := term.GetSize(output.Fd())
	if err != nil {
		return errors.New("не удалось определить размер терминала; используйте команды profile")
	}
	programOptions := []tea.ProgramOption{
		tea.WithInput(input), tea.WithOutput(output), tea.WithWindowSize(width, height),
		tea.WithFilter(deferExitWhileBusy),
	}
	if os.Getenv("NO_COLOR") != "" {
		programOptions = append(programOptions, tea.WithColorProfile(colorprofile.ASCII))
	}
	final, err := tea.NewProgram(newModel(profiles, options, width, height), programOptions...).Run()
	if err != nil {
		return err
	}
	finished := final.(model)
	if finished.failure != nil {
		// Diagnostics are printed after leaving the alternate screen. Sanitize
		// that final line too, including any file path supplied by the caller.
		return errors.New(safeText(finished.errorText()))
	}
	return nil
}

// The framework handles external SIGINT/SIGTERM before Update. Route them
// through the same graceful-exit path as keyboard Escape/Ctrl+C.
func deferExitWhileBusy(current tea.Model, msg tea.Msg) tea.Msg {
	switch msg.(type) {
	case tea.QuitMsg, tea.InterruptMsg:
		if m, ok := current.(model); ok && m.busy {
			return exitRequested{}
		}
	}
	return msg
}
