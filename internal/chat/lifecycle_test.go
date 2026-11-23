package chat

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/bacrehclb/ShadowTalk/internal/protocol"
)

func TestPendingConnectStopsOnLastCancellationTimeoutAndClose(t *testing.T) {
	for _, mode := range []string{"cancel", "timeout", "close"} {
		t.Run(mode, func(t *testing.T) {
			source := newSource()
			entered, stopped := make(chan struct{}), make(chan struct{})
			source.connect = func(ctx context.Context, _ string) (Session, error) {
				close(entered)
				<-ctx.Done()
				close(stopped)
				return nil, ctx.Err()
			}
			s := start(t, source, Options{MaxDialogs: 1, ConnectTimeout: 150 * time.Millisecond})
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			result := make(chan error, 1)
			go func() { _, err := s.Connect(ctx, "alice"); result <- err }()
			await(t, entered)
			if _, err := s.Connect(t.Context(), "other"); !errors.Is(err, ErrDialogLimit) || source.dials.Load() != 1 {
				t.Fatal("pending attempts bypassed limit")
			}
			want := error(context.DeadlineExceeded)
			switch mode {
			case "cancel":
				cancel()
				want = context.Canceled
			case "close":
				_ = s.Close()
				want = net.ErrClosed
			}
			await(t, stopped)
			err := <-result
			if mode == "close" && errors.Is(err, context.Canceled) {
				return
			}
			if !errors.Is(err, want) {
				t.Fatalf("got %v, want %v", err, want)
			}
		})
	}
}

func TestUnreadEventQueueAppliesBackpressureAndNeverBlocksClose(t *testing.T) {
	a, b := pairSources(t, nil)
	alice, bob := start(t, a, Options{}), start(t, b, Options{EventBuffer: 1})
	out, in := connect(t, alice, "bob"), findDialog(t, bob, "alice")
	first, second := message(t, "first"), message(t, "second")
	if err := out.Send(t.Context(), first); err != nil {
		t.Fatal(err)
	}
	if err := out.Send(t.Context(), second); err != nil {
		t.Fatal(err)
	}
	// Bob's reader now holds the second frame while its one-slot queue holds
	// the first. A third write cannot proceed until events are consumed.
	ctx, cancel := context.WithTimeout(t.Context(), 150*time.Millisecond)
	defer cancel()
	if err := out.Send(ctx, message(t, "third")); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("no backpressure: %v", err)
	}
	if len(bob.events) != 1 {
		t.Fatal("queue grew beyond its capacity")
	}
	finished := make(chan error, 1)
	go func() { finished <- in.Close() }()
	select {
	case err := <-finished:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("close waited for UI event consumption")
	}
	await(t, out.Done())
	if err := bob.Close(); err != nil {
		t.Fatal(err)
	}
	var queued []Event
	for e := range bob.Events() {
		queued = append(queued, e)
	}
	if len(queued) != 1 || queued[0].Message != first {
		t.Fatal("already queued data was lost during shutdown")
	}
}

func TestCancelWaitingSenderDoesNotAbortActiveWrite(t *testing.T) {
	source := newSource()
	s := start(t, source, Options{})
	raw := incoming(t, source, contact(t, "alice"))
	d := findDialog(t, s, "alice")
	first := message(t, "first")
	finished := make(chan error, 1)
	go func() { finished <- d.Send(t.Context(), first) }()
	// Taking one byte proves the first sender acquired the dialog writer;
	// preserve it for the protocol reader used to finish that same frame.
	var prefix [1]byte
	_ = raw.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, err := io.ReadFull(raw, prefix[:]); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	if err := d.Send(ctx, message(t, "canceled waiter")); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	frame, err := protocol.NewReader(io.MultiReader(bytes.NewReader(prefix[:]), raw)).ReadFrame()
	if err != nil || frame != first.frame() {
		t.Fatalf("active sender damaged: %v", err)
	}
	if err := <-finished; err != nil {
		t.Fatal(err)
	}
	if d.ctx.Err() != nil {
		t.Fatal("waiting sender closed active stream")
	}
}

func TestIdleConversationSurvivesButPartialFrameTimesOut(t *testing.T) {
	source := newSource()
	s := start(t, source, Options{FrameTimeout: 100 * time.Millisecond})
	raw := incoming(t, source, contact(t, "alice"))
	d := findDialog(t, s, "alice")
	select {
	case <-d.Done():
		t.Fatal("idle conversation timed out")
	case <-time.After(200 * time.Millisecond):
	}
	if _, err := raw.Write([]byte{0}); err != nil {
		t.Fatal(err)
	}
	await(t, d.Done())
	if !errors.Is(d.Err(), os.ErrDeadlineExceeded) {
		t.Fatalf("partial frame did not time out: %v", d.Err())
	}
}

func TestMalformedFrameClosesOnlyItsDialog(t *testing.T) {
	source := newSource()
	s := start(t, source, Options{})
	bad := incoming(t, source, contact(t, "alice"))
	d := findDialog(t, s, "alice")
	good := incoming(t, source, contact(t, "carol"))
	findDialog(t, s, "carol")
	var header [4]byte
	binary.BigEndian.PutUint32(header[:], protocol.MaxFrameBytes+1)
	if _, err := bad.Write(header[:]); err != nil {
		t.Fatal(err)
	}
	await(t, d.Done())
	if !errors.Is(d.Err(), protocol.ErrFrameTooLarge) {
		t.Fatal(d.Err())
	}
	m := message(t, "other dialog remains usable")
	if err := protocol.NewWriter(good).WriteFrame(m.frame()); err != nil {
		t.Fatal(err)
	}
	if e := event(t, s); e.Peer.Alias != "carol" || e.Message != m {
		t.Fatal("bad peer affected another conversation")
	}
}

func TestShutdownAndSourceFailureReleaseWorkersWithoutEventConsumer(t *testing.T) {
	for _, mode := range []string{"close", "cancel", "source-failure"} {
		t.Run(mode, func(t *testing.T) {
			source := newSource()
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			s, err := Start(ctx, source, Options{EventBuffer: 1})
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			raw := incoming(t, source, contact(t, "alice"))
			d := findDialog(t, s, "alice")
			writer := protocol.NewWriter(raw)
			for range 2 {
				if err := writer.WriteFrame(message(t, "queued").frame()); err != nil {
					t.Fatal(err)
				}
			}
			want := errors.New("transport crashed")
			switch mode {
			case "close":
				want = nil
				var wg sync.WaitGroup
				for range 8 {
					wg.Go(func() {
						if err := s.Close(); err != nil {
							t.Error(err)
						}
					})
				}
				wg.Wait()
			case "cancel":
				cancel()
				want = context.Canceled
			case "source-failure":
				source.fail(want)
			}
			await(t, s.Done())
			await(t, d.Done())
			await(t, source.closed)
			if !errors.Is(s.Err(), want) {
				t.Fatalf("terminal cause: %v, want %v", s.Err(), want)
			}
			if len(s.Dialogs()) != 0 {
				t.Fatal("shutdown retained active conversations")
			}
			for range s.Events() {
			}
			for range s.Changes() {
			}
		})
	}
}

func TestInvalidStartupDoesNotTakeOwnership(t *testing.T) {
	for _, options := range []Options{{MaxDialogs: -1}, {MaxDialogs: 1025}, {EventBuffer: -1}, {EventBuffer: 4097}, {WriteTimeout: -1}, {FrameTimeout: -1}, {ConnectTimeout: -1}} {
		source := newSource()
		if _, err := Start(t.Context(), source, options); !errors.Is(err, ErrInvalidConfig) {
			t.Fatal(err)
		}
		select {
		case <-source.closed:
			t.Fatal("invalid startup closed caller's source")
		default:
		}
	}
	if _, err := Start(nil, newSource(), Options{}); !errors.Is(err, ErrInvalidConfig) {
		t.Fatal(err)
	}
	if _, err := Start(t.Context(), nil, Options{}); !errors.Is(err, ErrInvalidConfig) {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := Start(ctx, newSource(), Options{}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
