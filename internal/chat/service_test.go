package chat

import (
	"context"
	"errors"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bacrehclb/ShadowTalk/internal/contacts"
	"github.com/bacrehclb/ShadowTalk/internal/protocol"
)

func TestBidirectionalMessagesReuseOneDialogAndDoNotAcknowledge(t *testing.T) {
	a, b := pairSources(t, nil)
	alice, bob := start(t, a, Options{}), start(t, b, Options{})
	out, in := connect(t, alice, "bob"), findDialog(t, bob, "alice")
	if connect(t, alice, "bob") != out || a.dials.Load() != 1 {
		t.Fatal("reused dialog redialed")
	}
	metadata := out.Peer()
	metadata.Alias = "changed"
	metadata.PublicKey[0]++
	if out.Peer().Alias != "bob" {
		t.Fatal("mutable contact metadata")
	}
	first := message(t, "Привет 👋\n\x1b[31m")
	if err := out.Send(t.Context(), first); err != nil {
		t.Fatal(err)
	}
	received := event(t, bob)
	if received.Kind != MessageReceived || received.Peer != in.Peer() || received.Message != first {
		t.Fatalf("wrong incoming event: %+v", received)
	}
	reply := message(t, "answer")
	if err := in.Send(t.Context(), reply); err != nil {
		t.Fatal(err)
	}
	if e := event(t, alice); e.Kind != MessageReceived || e.Message != reply {
		t.Fatalf("reply or unexpected automatic ACK: %+v", e)
	}
	// Repeated IDs are still data events. Durable deduplication belongs later.
	if err := out.Send(t.Context(), first); err != nil {
		t.Fatal(err)
	}
	if e := event(t, bob); e.Message != first {
		t.Fatal("core silently discarded a duplicate ID")
	}
	select {
	case e := <-alice.Events():
		t.Fatalf("unexpected event: %+v", e)
	default:
	}
	if err := out.Close(); err != nil {
		t.Fatal(err)
	}
	await(t, in.Done())
	if out.Err() != nil || len(alice.Dialogs()) != 0 || len(bob.Dialogs()) != 0 {
		t.Fatal("close did not remove the conversation")
	}
	reopened := connect(t, alice, "bob")
	if reopened == out {
		t.Fatal("reused a closed conversation handle")
	}
}

func TestCrossedDialsShareDialogsAndKeepBothStreams(t *testing.T) {
	var entered atomic.Int32
	barrier := make(chan struct{})
	a, b := pairSources(t, func(ctx context.Context) error {
		if entered.Add(1) == 2 {
			close(barrier)
		}
		select {
		case <-barrier:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	})
	alice, bob := start(t, a, Options{}), start(t, b, Options{})
	type result struct {
		dialog *Dialog
		err    error
	}
	results := make(chan result, 2)
	go func() { d, err := alice.Connect(t.Context(), "bob"); results <- result{d, err} }()
	go func() { d, err := bob.Connect(t.Context(), "alice"); results <- result{d, err} }()
	for range 2 {
		if r := <-results; r.err != nil {
			t.Fatal(r.err)
		}
	}
	out, in := findDialog(t, alice, "bob"), findDialog(t, bob, "alice")
	for _, d := range []*Dialog{out, in} {
		waitLinks(t, d, 2)
	}
	if len(alice.Dialogs()) != 1 || len(bob.Dialogs()) != 1 {
		t.Fatal("duplicate dialogs")
	}
	const count = 24
	var wg sync.WaitGroup
	for range count {
		ma, mb := message(t, "alice"), message(t, "bob")
		wg.Go(func() {
			if err := out.Send(t.Context(), ma); err != nil {
				t.Error(err)
			}
		})
		wg.Go(func() {
			if err := in.Send(t.Context(), mb); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	for _, s := range []*Service{alice, bob} {
		seen := make(map[protocol.MessageID]bool)
		for range count {
			e := event(t, s)
			if e.Kind != MessageReceived || seen[e.Message.ID] {
				t.Fatal("lost message boundary or duplicate event")
			}
			seen[e.Message.ID] = true
		}
	}
	// Losing one crossed connection keeps the existing other stream usable.
	out.mu.Lock()
	primary := out.primary
	out.mu.Unlock()
	_ = primary.close()
	waitLinks(t, out, 1)
	waitLinks(t, in, 1)
	after := message(t, "remaining stream")
	if err := out.Send(t.Context(), after); err != nil {
		t.Fatal(err)
	}
	if e := event(t, bob); e.Message != after {
		t.Fatal("remaining stream lost message")
	}
	if a.dials.Load() != 1 || b.dials.Load() != 1 {
		t.Fatal("stream failure triggered an automatic redial")
	}
	if err := out.Close(); err != nil {
		t.Fatal(err)
	}
	await(t, in.Done())
}

func waitLinks(t *testing.T, d *Dialog, count int) {
	t.Helper()
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		d.mu.Lock()
		n := len(d.links)
		d.mu.Unlock()
		if n == count {
			return
		}
		select {
		case <-ticker.C:
		case <-timer.C:
			t.Fatalf("streams=%d, want %d", n, count)
		}
	}
}

func TestDuplicateIncomingIsClosedWithoutReplacingConversation(t *testing.T) {
	source := newSource()
	s := start(t, source, Options{})
	peer := contact(t, "alice")
	first := incoming(t, source, peer)
	d := findDialog(t, s, "alice")
	duplicate := incoming(t, source, peer)
	_ = duplicate.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, err := duplicate.Read(make([]byte, 1)); err == nil {
		t.Fatal("duplicate remained open")
	}
	m := message(t, "first connection still works")
	if err := protocol.NewWriter(first).WriteFrame(m.frame()); err != nil {
		t.Fatal(err)
	}
	if e := event(t, s); e.Message != m || findDialog(t, s, "alice") != d {
		t.Fatal("duplicate replaced active dialog")
	}
}

func TestAckIsOnlyAnAttributedPeerClaim(t *testing.T) {
	source := newSource()
	s := start(t, source, Options{})
	peer := contact(t, "alice")
	raw := incoming(t, source, peer)
	claim := message(t, "not sent by this core")
	if err := protocol.NewWriter(raw).WriteFrame(protocol.Frame{Type: protocol.TypeAck, ID: claim.ID}); err != nil {
		t.Fatal(err)
	}
	e := event(t, s)
	if e.Kind != AcknowledgementReceived || e.Peer.PublicKey != peer.PublicKey || e.Message.ID != claim.ID || e.Message.Text != "" {
		t.Fatal("ack was misattributed or transformed into delivery status")
	}
}

func TestConnectCoalescesAndCancelingOneWaiterDoesNotCancelOthers(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	a, b := pairSources(t, func(ctx context.Context) error {
		close(entered)
		select {
		case <-release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	})
	alice, bob := start(t, a, Options{}), start(t, b, Options{})
	firstCtx, cancel := context.WithCancel(t.Context())
	first := make(chan error, 1)
	go func() { _, err := alice.Connect(firstCtx, "bob"); first <- err }()
	await(t, entered)
	type result struct {
		d   *Dialog
		err error
	}
	second := make(chan result, 1)
	go func() { d, err := alice.Connect(t.Context(), "bob"); second <- result{d, err} }()
	waitWaiters(t, alice, "bob", 2)
	cancel()
	if err := <-first; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	close(release)
	r := <-second
	if r.err != nil || r.d == nil || a.dials.Load() != 1 {
		t.Fatalf("shared dial: %v, dials=%d", r.err, a.dials.Load())
	}
	findDialog(t, bob, "alice")
}

func waitWaiters(t *testing.T, s *Service, alias string, count int) {
	t.Helper()
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		s.mu.Lock()
		a := s.connecting[alias]
		n := 0
		if a != nil {
			n = a.waiters
		}
		s.mu.Unlock()
		if n == count {
			return
		}
		select {
		case <-ticker.C:
		case <-timer.C:
			t.Fatalf("waiters=%d, want %d", n, count)
		}
	}
}

func TestInvalidContactsAndDialogLimitsDoNotDial(t *testing.T) {
	a, b := pairSources(t, nil)
	alice := start(t, a, Options{MaxDialogs: 1})
	_ = start(t, b, Options{})
	if _, err := alice.Connect(t.Context(), "bad name"); !errors.Is(err, contacts.ErrInvalidContact) || a.dials.Load() != 0 {
		t.Fatal(err)
	}
	if _, err := alice.Connect(t.Context(), "unknown"); !errors.Is(err, contacts.ErrContactNotFound) {
		t.Fatal(err)
	}
	d := connect(t, alice, "bob")
	before := a.dials.Load()
	if _, err := alice.Connect(t.Context(), "carol"); !errors.Is(err, ErrDialogLimit) || a.dials.Load() != before {
		t.Fatal(err)
	}
	rejected := incoming(t, a, contact(t, "carol"))
	_ = rejected.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, err := rejected.Read(make([]byte, 1)); err == nil {
		t.Fatal("incoming dialog bypassed limit")
	}
	if err := d.Send(t.Context(), Message{}); !errors.Is(err, protocol.ErrInvalidFrame) {
		t.Fatal(err)
	}
	if _, err := NewMessage(""); !errors.Is(err, protocol.ErrInvalidText) {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := d.Send(ctx, message(t, "canceled")); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if d.ctx.Err() != nil {
		t.Fatal("local validation/cancellation destroyed dialog")
	}
	if err := alice.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := alice.Connect(t.Context(), "bob"); !errors.Is(err, net.ErrClosed) {
		t.Fatal(err)
	}
}
