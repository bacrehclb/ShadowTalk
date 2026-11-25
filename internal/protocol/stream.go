package protocol

import (
	"encoding/binary"
	"io"
	"sync"
)

// Reader reads one frame at a time without buffering bytes from the next frame.
// Use a single Reader per stream; concurrent calls are serialized. Every error,
// including a timeout or EOF, is terminal and repeated without further I/O.
// Reader does not own the stream: the caller must close it on any read failure.
type Reader struct {
	mu     sync.Mutex
	source io.Reader
	err    error
}

func NewReader(source io.Reader) *Reader { return &Reader{source: source} }

func (r *Reader) ReadFrame() (Frame, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.err != nil {
		return Frame{}, r.err
	}
	frame, err := r.read()
	if err != nil {
		r.err = err
	}
	return frame, err
}

func (r *Reader) read() (Frame, error) {
	if r.source == nil {
		return Frame{}, ErrInvalidConfig
	}
	var header [4]byte
	if _, err := io.ReadFull(r.source, header[:]); err != nil {
		return Frame{}, err
	}
	size := binary.BigEndian.Uint32(header[:])
	if size == 0 {
		return Frame{}, ErrInvalidFrame
	}
	if size > MaxFrameBytes {
		return Frame{}, ErrFrameTooLarge
	}
	body := make([]byte, int(size))
	if _, err := io.ReadFull(r.source, body); err != nil {
		if err == io.EOF {
			err = io.ErrUnexpectedEOF // A complete header already began this frame.
		}
		return Frame{}, err
	}
	return decode(body)
}

// Writer serializes complete frames. Use one Writer per stream, alongside its
// Reader. Invalid local frames cause no I/O and allow a corrected retry. Any
// write failure is terminal, even if all bytes might have reached the peer;
// later calls return that error without writing. The caller must close the
// stream and let delivery retry the same message ID on a new connection.
// Deadlines, cancellation and stream ownership remain with the caller.
type Writer struct {
	mu          sync.Mutex
	destination io.Writer
	err         error
}

func NewWriter(destination io.Writer) *Writer { return &Writer{destination: destination} }

func (w *Writer) WriteFrame(frame Frame) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.err != nil {
		return w.err
	}
	if w.destination == nil {
		return ErrInvalidConfig
	}
	body, err := encode(frame)
	if err != nil {
		return err
	}
	packet := make([]byte, 4+len(body))
	binary.BigEndian.PutUint32(packet[:4], uint32(len(body)))
	copy(packet[4:], body)
	for len(packet) != 0 {
		n, err := w.destination.Write(packet)
		if n < 0 || n > len(packet) || n == 0 && err == nil {
			err = io.ErrShortWrite
		}
		if err != nil {
			w.err = err
			return err
		}
		packet = packet[n:]
	}
	return nil
}
