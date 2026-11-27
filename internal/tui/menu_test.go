package tui

import (
	"errors"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/bacrehclb/ShadowTalk/internal/contacts"
	"github.com/bacrehclb/ShadowTalk/internal/identity"
	"github.com/charmbracelet/x/ansi"
)

type fakeMenu struct {
	token                   string
	imports, saves, removes int
	settings                TorSettings
	failure                 error
}

func (s *fakeMenu) CreateInvitation(time.Duration) (string, error) { return s.token, s.failure }
func (s *fakeMenu) ImportContact(token, alias, fp string) error {
	if _, err := contacts.ConfirmInvitation(token, alias, fp, time.Now()); err != nil {
		return err
	}
	s.imports++
	return s.failure
}
func (s *fakeMenu) ListContacts() ([]contacts.Contact, error) { return nil, s.failure }
func (s *fakeMenu) RemoveContact(string, string) error        { s.removes++; return s.failure }
func (s *fakeMenu) LoadTor() (TorSettings, error)             { return s.settings, s.failure }
func (s *fakeMenu) SaveTor(v TorSettings) error               { s.saves++; s.settings = v; return s.failure }
func (s *fakeMenu) SaveInvitation(string, string) error       { return s.failure }
func (s *fakeMenu) ReadInvitation(string) (string, error)     { return s.token, s.failure }

func newTestMenu(t *testing.T) (*menuModel, *fakeMenu) {
	t.Helper()
	keys, err := identity.GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	token, err := contacts.CreateInvitation(keys, "Alice_01", time.Now(), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	s := &fakeMenu{token: token, settings: TorSettings{Mode: "direct", Timeout: "3m"}}
	return newMenu(s, MenuOptions{Directory: "/tmp/profile", DescribeError: func(err error) string { return err.Error() }}, 80, 24), s
}

func TestMenuImportRequiresFingerprintAndCancelNeverSaves(t *testing.T) {
	m, s := newTestMenu(t)
	m.selected = 3
	m.choose()
	m.fields[0].SetValue(s.token)
	m.submit()
	if m.screen != menuConfirm || m.fields[1].Value() != "" {
		t.Fatal("missing explicit confirmation")
	}
	if cmd := m.submit(); cmd != nil || s.imports != 0 {
		t.Fatal("unconfirmed import")
	}
	m.fields[1].SetValue(m.invite.Fingerprint())
	cmd := m.submit()
	if cmd == nil || !m.busy {
		t.Fatal("missing async import")
	}
	if _, duplicate := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter}); duplicate != nil {
		t.Fatal("double submission")
	}
	m.Update(cmd())
	if m.screen != menuHome || s.imports != 1 {
		t.Fatal("import failed")
	}
	m.selected = 3
	m.choose()
	m.fields[0].SetValue(s.token)
	m.submit()
	m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	if s.imports != 1 || m.token != "" {
		t.Fatal("cancel saved or retained token")
	}
}

func TestMenuTorPasteAndCancelPreserveSettings(t *testing.T) {
	m, s := newTestMenu(t)
	m.selected = 5
	cmd := m.choose()
	m.Update(cmd())
	m.selected = 1
	m.choose()
	m.Update(tea.PasteMsg{Content: "first bridge\nsecond bridge"})
	if m.bridges.Value() != "first bridge\nsecond bridge" {
		t.Fatal("multiline paste lost")
	}
	m.Update(tea.PasteMsg{Content: strings.Repeat("x", 9001)})
	if m.bridges.Value() != "first bridge\nsecond bridge" {
		t.Fatal("oversized paste truncated and inserted")
	}
	m.key("ctrl+s")
	m.fields[0].SetValue("5m")
	m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.screen != menuBridges || m.tor.Timeout != "5m" || m.bridges.Value() != "first bridge\nsecond bridge" {
		t.Fatal("back from Tor options lost draft")
	}
	m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	if s.saves != 0 {
		t.Fatal("cancel wrote settings")
	}
}

func TestMenuWaitsForSaveBeforeInterrupt(t *testing.T) {
	m, s := newTestMenu(t)
	m.tor = TorSettings{Mode: "direct", Timeout: "5m"}
	m.torForm()
	cmd := m.submit()
	if _, quit := m.Update(menuExitFilter(m, tea.InterruptMsg{})); quit != nil || !m.quitting {
		t.Fatal("exited during write")
	}
	_, quit := m.Update(cmd())
	if s.saves != 1 || quit == nil {
		t.Fatal("save not drained")
	}
}

func TestMenuViewsKeepConfirmationFieldsAndSanitizeDiagnostics(t *testing.T) {
	m, s := newTestMenu(t)
	m.selected = 3
	m.choose()
	m.fields[0].SetValue(s.token)
	m.submit()
	m.Update(tea.WindowSizeMsg{Width: 60, Height: 20})
	m.problem = "ошибка\x1b]52;c;PRIVATE\a"
	v := m.View().Content
	if strings.Contains(v, "\x1b]52") || !strings.Contains(ansi.Strip(v), "Отпечаток от собеседника") || !strings.Contains(ansi.Strip(v), "Esc") {
		t.Fatal("unsafe or inaccessible form", ansi.Strip(v))
	}
	m.home("")
	m.selected = 2
	m.choose()
	s.failure = errors.New("save failed")
	cmd := m.submit()
	m.Update(cmd())
	if m.problem == "" || m.screen != menuTTL || m.busy {
		t.Fatal("error hidden")
	}
}

func TestMenuKeepsSpacesInPastedInvitationFilePath(t *testing.T) {
	m, _ := newTestMenu(t)
	m.selected = 3
	m.choose()
	m.focusField(1)
	m.Update(tea.PasteMsg{Content: " /tmp/приглашение Alice.invite "})
	if m.fields[1].Value() != "/tmp/приглашение Alice.invite" {
		t.Fatal(m.fields[1].Value())
	}
}
