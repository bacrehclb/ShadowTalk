package protocol

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"os"
	"sync"
	"testing"
)

func packet(body []byte) []byte {
	result := make([]byte, 4+len(body))
	binary.BigEndian.PutUint32(result, uint32(len(body)))
	copy(result[4:], body)
	return result
}

type chunkReader struct {
	source io.Reader
	size   int
	calls  int
}

func (r *chunkReader) Read(p []byte) (int, error) {
	r.calls++
	return r.source.Read(p[:min(len(p), r.size)])
}

type chunkWriter struct {
	bytes.Buffer
	size  int
	calls int
}

func (w *chunkWriter) Write(p []byte) (int, error) {
	w.calls++
	return w.Buffer.Write(p[:min(len(p), w.size)])
}

func TestFragmentedAndCoalescedFrames(t *testing.T) {
	first, second := textFrame(t, "Привет"), textFrame(t, "next\nline")
	second.ID[0]++
	frames := []Frame{first, {Type: TypeAck, ID: first.ID}, second}
	var whole bytes.Buffer
	writer := NewWriter(&whole)
	for _, frame := range frames {
		if err := writer.WriteFrame(frame); err != nil {
			t.Fatal(err)
		}
	}
	if got := binary.BigEndian.Uint32(whole.Bytes()[:4]); got != uint32(len(`{"version":1,"type":"text","id":"`+fixtureID+`","text":"Привет"}`)) {
		t.Fatal("length prefix is not the JSON byte count")
	}
	for _, size := range []int{1, 2, 3, 4, 7, whole.Len()} {
		source := &chunkReader{source: bytes.NewReader(whole.Bytes()), size: size}
		reader := NewReader(source)
		for _, want := range frames {
			if got, err := reader.ReadFrame(); err != nil || got != want {
				t.Fatalf("chunk=%d: %+v, %v", size, got, err)
			}
		}
		if _, err := reader.ReadFrame(); err != io.EOF {
			t.Fatalf("frame boundary EOF: %v", err)
		}
		calls := source.calls
		if _, err := reader.ReadFrame(); err != io.EOF || source.calls != calls {
			t.Fatal("reader continued after EOF")
		}
	}
	short := &chunkWriter{size: 1}
	writer = NewWriter(short)
	for _, frame := range frames {
		if err := writer.WriteFrame(frame); err != nil {
			t.Fatal(err)
		}
	}
	if !bytes.Equal(short.Bytes(), whole.Bytes()) {
		t.Fatal("partial writes corrupted frame boundaries")
	}
}

func TestEveryTruncationFailsWithoutReturningPartialData(t *testing.T) {
	body, err := encode(textFrame(t, "hello"))
	if err != nil {
		t.Fatal(err)
	}
	encoded := packet(body)
	for end := 0; end < len(encoded); end++ {
		source := &chunkReader{source: bytes.NewReader(encoded[:end]), size: len(encoded)}
		reader := NewReader(source)
		want := io.ErrUnexpectedEOF
		if end == 0 {
			want = io.EOF
		}
		frame, err := reader.ReadFrame()
		if frame != (Frame{}) || err != want {
			t.Fatalf("truncated at %d: %+v, %v", end, frame, err)
		}
		calls := source.calls
		if _, err := reader.ReadFrame(); err != want || calls != source.calls {
			t.Fatal("reader resumed a truncated frame")
		}
	}
}

type headerOnlyReader struct {
	header []byte
	calls  int
}

func (r *headerOnlyReader) Read(p []byte) (int, error) {
	r.calls++
	if len(r.header) == 0 {
		return 0, errors.New("body must not be read")
	}
	n := copy(p, r.header)
	r.header = r.header[n:]
	return n, nil
}

func TestInvalidLengthsAreRejectedBeforeBodyRead(t *testing.T) {
	for _, size := range []uint32{0, MaxFrameBytes + 1, 1 << 31, ^uint32(0)} {
		header := make([]byte, 4)
		binary.BigEndian.PutUint32(header, size)
		source := &headerOnlyReader{header: header}
		reader := NewReader(source)
		want := ErrFrameTooLarge
		if size == 0 {
			want = ErrInvalidFrame
		}
		if frame, err := reader.ReadFrame(); frame != (Frame{}) || !errors.Is(err, want) || source.calls != 1 {
			t.Fatalf("size %d: %v, reads %d", size, err, source.calls)
		}
		if _, err := reader.ReadFrame(); !errors.Is(err, want) || source.calls != 1 {
			t.Fatal("invalid prefix was not terminal")
		}
	}
	// The maximum declared length is allowed but still requires a valid body.
	source := bytes.NewReader(packet(bytes.Repeat([]byte{'x'}, MaxFrameBytes)))
	if _, err := NewReader(source).ReadFrame(); !errors.Is(err, ErrInvalidFrame) || errors.Is(err, ErrFrameTooLarge) || source.Len() != 0 {
		t.Fatalf("maximum frame: %v", err)
	}
}

func TestInvalidFrameDoesNotConsumeOrResynchronizeToFollowingFrame(t *testing.T) {
	valid, _ := encode(textFrame(t, "hello"))
	next := packet(valid)
	source := bytes.NewReader(append(packet([]byte(`{"version":2}`)), next...))
	reader := NewReader(source)
	for range 2 {
		if frame, err := reader.ReadFrame(); frame != (Frame{}) || !errors.Is(err, ErrUnsupportedVersion) || source.Len() != len(next) {
			t.Fatalf("continued after bad frame: %v", err)
		}
	}
}

type readFailure struct {
	calls  int
	prefix []byte
	err    error
}

func (r *readFailure) Read(p []byte) (int, error) {
	r.calls++
	n := copy(p, r.prefix)
	r.prefix = r.prefix[n:]
	return n, r.err
}

func TestReadTimeoutIsPreservedAndTerminal(t *testing.T) {
	for _, prefix := range [][]byte{nil, {0, 0}} {
		source := &readFailure{prefix: prefix, err: os.ErrDeadlineExceeded}
		reader := NewReader(source)
		for range 2 {
			if frame, err := reader.ReadFrame(); frame != (Frame{}) || !errors.Is(err, os.ErrDeadlineExceeded) {
				t.Fatal(err)
			}
		}
		if source.calls != 1 {
			t.Fatal("reader retried after timeout")
		}
	}
}

type writeFailure struct {
	count int
	err   error
	calls int
}

func (w *writeFailure) Write(p []byte) (int, error) {
	w.calls++
	if w.count == -2 {
		return len(p), w.err
	}
	return w.count, w.err
}

func TestWriteFailuresAreTerminal(t *testing.T) {
	frame := textFrame(t, "hello")
	for _, tt := range []struct {
		name      string
		n         int
		err, want error
	}{
		{"zero-progress", 0, nil, io.ErrShortWrite},
		{"negative-count", -1, nil, io.ErrShortWrite},
		{"excess-count", MaxFrameBytes + 5, nil, io.ErrShortWrite},
		{"partial-timeout", 2, os.ErrDeadlineExceeded, os.ErrDeadlineExceeded},
		{"all-bytes-and-error", -2, io.ErrClosedPipe, io.ErrClosedPipe},
	} {
		t.Run(tt.name, func(t *testing.T) {
			destination := &writeFailure{count: tt.n, err: tt.err}
			writer := NewWriter(destination)
			for range 2 {
				if err := writer.WriteFrame(frame); !errors.Is(err, tt.want) {
					t.Fatal(err)
				}
			}
			if destination.calls != 1 {
				t.Fatal("writer retried after failure")
			}
		})
	}
}

func TestInvalidLocalFrameDoesNotWriteAndAllowsCorrection(t *testing.T) {
	var destination bytes.Buffer
	writer := NewWriter(&destination)
	if err := writer.WriteFrame(Frame{}); !errors.Is(err, ErrInvalidFrame) || destination.Len() != 0 {
		t.Fatal("invalid frame emitted bytes")
	}
	frame := textFrame(t, "valid")
	if err := writer.WriteFrame(frame); err != nil {
		t.Fatal(err)
	}
	if got, err := NewReader(&destination).ReadFrame(); err != nil || got != frame {
		t.Fatal(err)
	}
	if _, err := NewReader(nil).ReadFrame(); !errors.Is(err, ErrInvalidConfig) {
		t.Fatal(err)
	}
	if err := NewWriter(nil).WriteFrame(frame); !errors.Is(err, ErrInvalidConfig) {
		t.Fatal(err)
	}
}

func TestConcurrentWritesPreserveWholeFrames(t *testing.T) {
	destination := &chunkWriter{size: 1}
	writer := NewWriter(destination)
	const count = 32
	var wg sync.WaitGroup
	for i := range count {
		frame := textFrame(t, "concurrent\nmessage")
		frame.ID[0] = byte(i + 1)
		wg.Go(func() {
			if err := writer.WriteFrame(frame); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	reader := NewReader(bytes.NewReader(destination.Bytes()))
	seen := make(map[MessageID]bool)
	for range count {
		frame, err := reader.ReadFrame()
		if err != nil || seen[frame.ID] || frame.Text != "concurrent\nmessage" {
			t.Fatalf("interleaved or duplicate frame: %v", err)
		}
		seen[frame.ID] = true
	}
	if _, err := reader.ReadFrame(); err != io.EOF {
		t.Fatal(err)
	}
}

func FuzzReadFrame(f *testing.F) {
	body, _ := encode(textFrame(f, "hello"))
	for _, seed := range [][]byte{packet(body), nil, {0, 0, 0, 0}, {255, 255, 255, 255}, packet([]byte("{}"))} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, input []byte) {
		source := bytes.NewReader(input)
		reader := NewReader(source)
		frame, err := reader.ReadFrame()
		if err != nil {
			if frame != (Frame{}) {
				t.Fatal("partial data escaped reader")
			}
			remaining := source.Len()
			if _, next := reader.ReadFrame(); next != err || source.Len() != remaining {
				t.Fatal("failed reader performed I/O")
			}
			return
		}
		var destination bytes.Buffer
		if err := NewWriter(&destination).WriteFrame(frame); err != nil {
			t.Fatal(err)
		}
		consumed := len(input) - source.Len()
		if !bytes.Equal(destination.Bytes(), input[:consumed]) {
			t.Fatal("frame changed after round trip")
		}
	})
}
