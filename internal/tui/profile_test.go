package tui

import (
	"errors"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/bacrehclb/ShadowTalk/internal/app"
	"github.com/bacrehclb/ShadowTalk/internal/identity"
	"github.com/charmbracelet/x/ansi"
)

type fakeProfiles struct {
	profile                            identity.Profile
	loadErr, writeErr                  error
	loads, creates, upgrades, recovers int
}

func (p *fakeProfiles) Load() (identity.Profile, error) {
	p.loads++
	return p.profile, p.loadErr
}
func (p *fakeProfiles) Create(nick string) (identity.Profile, error) {
	p.creates++
	if p.writeErr == nil {
		p.profile = sampleProfile()
		p.profile.Nickname = nick
		p.loadErr = nil
	}
	return p.profile, p.writeErr
}
func (p *fakeProfiles) Upgrade() (identity.Profile, error) {
	p.upgrades++
	if p.writeErr == nil {
		p.profile.PublicKey[0] = 1
	}
	return p.profile, p.writeErr
}
func (p *fakeProfiles) Recover() (identity.Profile, error) {
	p.recovers++
	return p.profile, p.writeErr
}

func sampleProfile() identity.Profile {
	p := identity.Profile{Nickname: "ivan_01", CreatedAt: time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)}
	p.PublicKey[0] = 1
	return p
}

func update(m model, msg tea.Msg) (model, tea.Cmd) {
	next, cmd := m.Update(msg)
	return next.(model), cmd
}

func initial(t *testing.T, profiles *fakeProfiles) model {
	t.Helper()
	m := newModel(profiles, Options{Directory: "/tmp/test", DescribeError: func(err error) string { return err.Error() }}, 80, 24)
	m, _ = update(m, m.Init()())
	return m
}

func enter(m model) (model, tea.Cmd) { return update(m, tea.KeyPressMsg{Code: tea.KeyEnter}) }

func TestCreateValidatesInputAndRunsOnlyOnce(t *testing.T) {
	p := &fakeProfiles{loadErr: identity.ErrProfileNotFound}
	m := initial(t, p)
	if m.screen != screenCreate || p.creates != 0 {
		t.Fatal("opening UI must not create a profile")
	}
	m, _ = update(m, tea.PasteMsg{Content: "bad.name"})
	m, cmd := enter(m)
	if cmd != nil || m.validation == "" || p.creates != 0 {
		t.Fatal("invalid nickname reached application")
	}
	m.nickname.SetValue("ivan_01")
	m, cmd = enter(m)
	if !m.busy || cmd == nil || p.creates != 0 {
		t.Fatal("operation must run asynchronously")
	}
	again, duplicate := enter(m)
	if duplicate != nil || !again.busy {
		t.Fatal("double Enter started another operation")
	}
	m, _ = update(m, cmd())
	if p.creates != 1 || m.screen != screenReady || m.busy || m.profile.Nickname != "ivan_01" {
		t.Fatal("profile creation did not reach ready screen")
	}
	if !strings.Contains(ansi.Strip(m.View().Content), p.profile.Fingerprint()) {
		t.Fatal("missing full fingerprint")
	}
}

func TestLegacyAndRecoveryRequireExplicitAction(t *testing.T) {
	for _, recovery := range []bool{false, true} {
		p := &fakeProfiles{profile: sampleProfile()}
		want, op := screenLegacy, opUpgrade
		if recovery {
			p.loadErr, want, op = app.ErrProfilePending, screenRecovery, opRecover
		} else {
			p.profile.PublicKey = [32]byte{}
		}
		m := initial(t, p)
		if m.screen != want || p.upgrades+p.recovers != 0 {
			t.Fatal("opening UI performed a mutation")
		}
		m, cmd := enter(m)
		if m.op != op || cmd == nil {
			t.Fatal("wrong action")
		}
		m, _ = update(m, cmd())
		if m.screen != screenReady || p.upgrades+p.recovers != 1 {
			t.Fatal("action did not finish")
		}
	}
}

func TestFailedMutationRechecksStateBeforeRetry(t *testing.T) {
	p := &fakeProfiles{loadErr: identity.ErrProfileNotFound, writeErr: errors.New("key store locked")}
	m := initial(t, p)
	m.nickname.SetValue("ivan_01")
	m, cmd := enter(m)
	m, _ = update(m, cmd())
	if m.screen != screenError || m.failure == nil {
		t.Fatal("failure hidden")
	}
	m, cmd = enter(m)
	if m.op != opLoad {
		t.Fatal("uncertain mutation was retried blindly")
	}
	m, _ = update(m, cmd())
	if m.screen != screenCreate || m.nickname.Value() != "ivan_01" || p.creates != 1 {
		t.Fatal("retry lost input or duplicated write")
	}
}

func TestMissingKeyNeverOffersNewIdentity(t *testing.T) {
	p := &fakeProfiles{loadErr: identity.ErrEncryptionKeyNotFound}
	m := initial(t, p)
	for range 2 {
		if m.screen != screenError {
			t.Fatal("missing key offered profile replacement")
		}
		var cmd tea.Cmd
		m, cmd = enter(m)
		m, _ = update(m, cmd())
	}
	if p.creates != 0 || p.upgrades != 0 || p.recovers != 0 {
		t.Fatal("missing key caused a mutation")
	}
}

func TestRecoveryCancellationAndConcurrentCompletionReload(t *testing.T) {
	for _, err := range []error{app.ErrCreationAborted, app.ErrNoPendingProfile, identity.ErrProfileExists} {
		p := &fakeProfiles{profile: sampleProfile(), loadErr: app.ErrProfilePending, writeErr: err}
		m := initial(t, p)
		m, cmd := enter(m)
		m, reload := update(m, cmd())
		if !m.busy || m.op != opLoad || reload == nil {
			t.Fatal("must discover current state by reading")
		}
		p.loadErr = nil
		m, _ = update(m, reload())
		if m.screen != screenReady || m.failure != nil || p.recovers != 1 {
			t.Fatal("reload failed")
		}
	}
}

func TestExitWaitsForInFlightWrite(t *testing.T) {
	p := &fakeProfiles{loadErr: identity.ErrProfileNotFound}
	m := initial(t, p)
	m.nickname.SetValue("ivan_01")
	m, write := enter(m)
	for _, request := range []tea.Msg{tea.KeyPressMsg{Code: tea.KeyEscape}, deferExitWhileBusy(m, tea.QuitMsg{}), deferExitWhileBusy(m, tea.InterruptMsg{})} {
		pending, quit := update(m, request)
		if quit != nil || !pending.quitting || !pending.busy {
			t.Fatal("exit abandoned the write")
		}
		result, quit := update(pending, write())
		if quit == nil {
			t.Fatal("exit never resumed")
		}
		if _, ok := quit().(tea.QuitMsg); !ok || result.busy || result.failure != nil {
			t.Fatal("wrong exit result")
		}
	}
}

func TestResponsiveViewsKeepActionsAndInput(t *testing.T) {
	for _, size := range [][2]int{{40, 20}, {80, 24}, {120, 40}} {
		for _, state := range []screen{screenCreate, screenReady, screenLegacy, screenRecovery, screenError} {
			m := initial(t, &fakeProfiles{profile: sampleProfile()})
			m.screen, m.failure = state, errors.New(strings.Repeat("длинная ошибка ", 100))
			m, _ = update(m, tea.WindowSizeMsg{Width: size[0], Height: size[1]})
			if state == screenCreate {
				m.nickname.SetValue("typed_nickname")
				m.validation = "Ник должен начинаться с буквы или цифры."
			}
			view := m.View()
			plain := ansi.Strip(view.Content)
			if !view.AltScreen || lipgloss.Height(view.Content) > size[1] || lipgloss.Width(view.Content) > size[0] {
				t.Fatalf("view overflow %v, screen %d", size, state)
			}
			if !strings.Contains(plain, "Enter") || !strings.Contains(plain, "Ctrl+C") {
				t.Fatalf("lost controls at %v", size)
			}
			if state == screenCreate && !strings.Contains(plain, "typed_nickname") {
				t.Fatal("input disappeared")
			}
		}
	}
}

func TestResizeDoesNotLoseTypedNickname(t *testing.T) {
	m := initial(t, &fakeProfiles{loadErr: identity.ErrProfileNotFound})
	m, _ = update(m, tea.PasteMsg{Content: "my_profile"})
	m, _ = update(m, tea.WindowSizeMsg{Width: 20, Height: 8})
	m, cmd := enter(m)
	if cmd != nil || !strings.Contains(m.View().Content, "Увеличьте") {
		t.Fatal("small screen accepted hidden action")
	}
	m, _ = update(m, tea.WindowSizeMsg{Width: 80, Height: 24})
	if m.nickname.Value() != "my_profile" {
		t.Fatal("resize lost nickname")
	}
	_, cmd = update(m, tea.KeyPressMsg{Code: tea.KeyEscape})
	if cmd == nil {
		t.Fatal("Escape did not quit")
	}
}

func TestExternalTextCannotControlTerminal(t *testing.T) {
	text := "folder\x1b]52;c;c2VjcmV0\a\n\u202efake"
	safe := safeText(text)
	if strings.ContainsAny(safe, "\x1b\a\n\u202e") {
		t.Fatal("terminal control sequence survived")
	}
	if safeText("профиль с пробелами") != "профиль с пробелами" {
		t.Fatal("ordinary Unicode path changed")
	}
}
