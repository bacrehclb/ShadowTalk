package tui

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/bacrehclb/ShadowTalk/internal/chat"
	"github.com/bacrehclb/ShadowTalk/internal/contacts"
	"github.com/bacrehclb/ShadowTalk/internal/delivery"
	"github.com/bacrehclb/ShadowTalk/internal/history"
	"github.com/bacrehclb/ShadowTalk/internal/identity"
	"github.com/bacrehclb/ShadowTalk/internal/protocol"
	"github.com/charmbracelet/x/ansi"
)

type fakeChat struct {
	changes chan struct{}
	closed  atomic.Int32
	sent    []chat.Message
	peers   [][32]byte
	sendErr error
}

func (s *fakeChat) Profile() identity.Profile { return sampleProfile() }
func (s *fakeChat) Contacts() []contacts.Contact {
	return []contacts.Contact{{Alias: "alice", PublicKey: [32]byte{2}}, {Alias: "bob", PublicKey: [32]byte{3}}}
}
func (s *fakeChat) History(context.Context, [32]byte, history.Query) (ChatPage, error) {
	return ChatPage{}, nil
}
func (s *fakeChat) Send(_ context.Context, p [32]byte, m chat.Message) error {
	s.sent = append(s.sent, m)
	s.peers = append(s.peers, p)
	return s.sendErr
}
func (s *fakeChat) Activity() map[[32]byte]uint64 { return nil }
func (s *fakeChat) Changes() <-chan struct{}      { return s.changes }
func (s *fakeChat) Err() error                    { return nil }
func (s *fakeChat) Close() error                  { s.closed.Add(1); return nil }

func readyChat(t *testing.T) (*chatModel, *fakeChat) {
	t.Helper()
	s := &fakeChat{changes: make(chan struct{})}
	r := newChatRuntime(func(context.Context, func(ChatStage)) (ChatSession, error) { return s, nil })
	t.Cleanup(func() { r.Close() })
	m := newChatModel(r, ChatOptions{}, 80, 24)
	m.Update(r.openCmd())
	m.loading = false
	return m, s
}
func keyPress(code rune, mod tea.KeyMod) tea.KeyPressMsg {
	return tea.KeyPressMsg{Code: code, Mod: mod}
}

func TestChatSendIsAsyncAndRetainsIDAcrossAmbiguousFailure(t *testing.T) {
	m, s := readyChat(t)
	m.Update(tea.PasteMsg{Content: "Привет\nsecond line"})
	_, send := m.Update(keyPress(tea.KeyEnter, 0))
	if send == nil || len(s.sent) != 0 || !m.draft().saving {
		t.Fatal("send was not deferred")
	}
	_, duplicate := m.Update(keyPress(tea.KeyEnter, 0))
	if duplicate != nil {
		t.Fatal("double Enter submitted twice")
	}
	s.sendErr = errors.New("ambiguous write")
	m.Update(send())
	m.Update(tea.PasteMsg{Content: "must not replace pending text"})
	if m.composer.Value() != "Привет\nsecond line" {
		t.Fatal("ambiguous draft was editable")
	}
	_, retry := m.Update(keyPress('r', tea.ModCtrl))
	if retry == nil {
		t.Fatal("retry unavailable")
	}
	s.sendErr = nil
	m.Update(retry())
	if len(s.sent) != 2 || s.sent[0] != s.sent[1] || m.draft().pending != nil || m.composer.Value() != "" {
		t.Fatal("retry changed ID/text or failed to clear draft")
	}
}

func TestChatSwitchKeepsDraftAndIgnoresStaleHistory(t *testing.T) {
	m, s := readyChat(t)
	m.Update(tea.PasteMsg{Content: "draft alice"})
	_, send := m.Update(keyPress(tea.KeyEnter, 0))
	oldRequest := m.request
	m.Update(keyPress('n', tea.ModCtrl))
	m.Update(tea.PasteMsg{Content: "draft bob"})
	m.Update(chatHistory{request: oldRequest, page: ChatPage{Entries: []ChatEntry{{Entry: history.Entry{Sequence: 123}}}}})
	if len(m.entries) != 0 {
		t.Fatal("old peer query overwrote current history")
	}
	m.Update(send())
	if m.composer.Value() != "draft bob" || s.peers[0] != ([32]byte{2}) {
		t.Fatal("send completion modified the new peer's draft")
	}
	m.Update(keyPress('p', tea.ModCtrl))
	if m.composer.Value() != "" {
		t.Fatal("successful old draft was not cleared")
	}
}

func TestChatRejectsOversizedPasteWithoutSendingTruncatedText(t *testing.T) {
	m, _ := readyChat(t)
	m.Update(tea.PasteMsg{Content: "kept"})
	m.Update(tea.PasteMsg{Content: strings.Repeat("é", protocol.MaxTextBytes)})
	if m.composer.Value() != "kept" || !strings.Contains(m.notice, "Вставка не добавлена") {
		t.Fatal("oversized paste was silently truncated")
	}
	m.Update(tea.PasteMsg{Content: "\x1b]52;c;payload\a\u202e"})
	if strings.ContainsAny(m.composer.Value(), "\x1b\a\u202e") {
		t.Fatal("unsafe composer contents")
	}
}

func TestChatHistoryPagingIsBoundedAndResizingPreservesDraft(t *testing.T) {
	m, _ := readyChat(t)
	m.Update(tea.PasteMsg{Content: "resize draft"})
	for page := 0; page < 6; page++ {
		rows := make([]ChatEntry, 50)
		for i := range rows {
			rows[i] = ChatEntry{Entry: history.Entry{Sequence: int64(1000 - page*50 - i), Message: history.Message{Peer: m.peer(), Text: "history line", RecordedAt: time.Now()}}}
		}
		m.request++
		m.Update(chatHistory{request: m.request, older: page != 0, page: ChatPage{Entries: rows, NextBefore: rows[49].Sequence}})
	}
	if len(m.entries) != maxChatEntries || m.nextBefore != 701 {
		t.Fatal("paging grew without bound or lost its cursor")
	}
	m.Update(tea.WindowSizeMsg{Width: 30, Height: 10})
	_, cmd := m.Update(keyPress(tea.KeyEnter, 0))
	if cmd != nil {
		t.Fatal("hidden small-screen action submitted")
	}
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	if m.composer.Value() != "resize draft" {
		t.Fatal("resize lost draft")
	}
}

func TestChatViewsBoundContentAndNeverRenderUntrustedControls(t *testing.T) {
	m, _ := readyChat(t)
	m.entries = []ChatEntry{{Entry: history.Entry{Sequence: 1, Message: history.Message{Direction: history.Incoming, Text: "hello\n\x1b]52;c;secret\a\x1b[2J\u202eevil", RecordedAt: time.Now()}}}, {Entry: history.Entry{Sequence: 2, Message: history.Message{Direction: history.Outgoing, Text: "reply", RecordedAt: time.Now()}}, State: delivery.Pending}}
	for _, size := range [][2]int{{60, 20}, {80, 24}, {120, 40}} {
		m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		m.viewport.GotoBottom()
		view := m.View()
		plain := ansi.Strip(view.Content)
		if lipgloss.Width(view.Content) > size[0] || lipgloss.Height(view.Content) > size[1] {
			t.Fatalf("chat overflow at %v", size)
		}
		if !strings.Contains(plain, "Enter") || !strings.Contains(plain, "Ctrl+C") {
			t.Fatalf("controls clipped at %v: %s", size, plain)
		}
		if strings.Contains(view.Content, "\x1b]52") || strings.Contains(view.Content, "\x1b[2J") || strings.ContainsAny(view.Content, "\a\u202e") {
			t.Fatal("untrusted terminal controls rendered")
		}
	}
}

func TestChatActivityMarksOtherContactsWithoutJumpingHistory(t *testing.T) {
	m, _ := readyChat(t)
	m.latest = false
	m.Update(chatChanged{activity: map[[32]byte]uint64{{3}: 1}})
	if !strings.Contains(ansi.Strip(m.View().Content), "• bob") || !strings.Contains(m.notice, "Есть изменения") || m.loading {
		t.Fatal("activity was invisible or jumped history")
	}
	m.selectContact(1)
	m.Update(chatHistory{request: m.request, page: ChatPage{}})
	if strings.Contains(ansi.Strip(m.View().Content), "• bob") || m.seen[[32]byte{3}] != 1 {
		t.Fatal("visited activity marker did not clear")
	}
}

func TestChatRuntimeWaitsForLateStartupAndInFlightSave(t *testing.T) {
	for _, during := range []string{"startup", "send"} {
		t.Run(during, func(t *testing.T) {
			entered, release := make(chan struct{}), make(chan struct{})
			s := &fakeChat{changes: make(chan struct{})}
			r := newChatRuntime(func(ctx context.Context, _ func(ChatStage)) (ChatSession, error) {
				if during == "startup" {
					close(entered)
					<-release
				}
				return s, nil
			})
			if during == "startup" {
				go r.openCmd()
			} else {
				r.openCmd()
				go func() {
					if !r.begin() {
						return
					}
					defer r.workers.Done()
					close(entered)
					<-release
				}()
			}
			<-entered
			done := make(chan error, 1)
			go func() { done <- r.Close() }()
			<-r.ctx.Done()
			if s.closed.Load() != 0 {
				t.Fatal("closed session during an owned operation")
			}
			close(release)
			select {
			case err := <-done:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(time.Second):
				t.Fatal("close did not finish")
			}
			if s.closed.Load() != 1 {
				t.Fatal("late session leaked or closed twice")
			}
			r.Close()
			if s.closed.Load() != 1 {
				t.Fatal("Close not idempotent")
			}
		})
	}
}
