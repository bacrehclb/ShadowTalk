package chat

import (
	"context"
	"errors"
	"net"
	"time"
)

// Send writes a previously created message exactly once. Success means the
// frame was written, not stored or read by the remote peer. An I/O error can
// occur after receipt, so preserve the ID; no automatic retry is attempted.
// Calls on one dialog are serialized. Cancellation before acquiring the writer
// leaves the connection usable; cancellation during I/O aborts that stream.
func (d *Dialog) Send(ctx context.Context, message Message) error {
	if ctx == nil {
		return ErrInvalidConfig
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	frame := message.frame()
	if err := frame.Validate(); err != nil {
		return err
	}
	d.mu.Lock()
	if d.closed || d.ctx.Err() != nil {
		d.mu.Unlock()
		return ErrDialogClosed
	}
	d.workers.Add(1)
	d.mu.Unlock()
	defer d.workers.Done()
	operation, cancel := context.WithTimeout(ctx, d.service.options.WriteTimeout)
	defer cancel()
	select {
	case <-operation.Done():
		return operation.Err()
	case <-d.ctx.Done():
		return ErrDialogClosed
	case <-d.writeGate:
	}
	defer func() { d.writeGate <- struct{}{} }()
	if err := operation.Err(); err != nil {
		return err
	}
	d.mu.Lock()
	active := d.primary
	if d.closed || d.ctx.Err() != nil || active == nil || active.ctx.Err() != nil {
		d.mu.Unlock()
		return ErrDialogClosed
	}
	d.mu.Unlock()
	deadline, _ := operation.Deadline()
	if err := active.session.SetWriteDeadline(deadline); err != nil {
		_ = active.close()
		return err
	}
	canceled := make(chan struct{})
	stop := context.AfterFunc(operation, func() { _ = active.close(); close(canceled) })
	err := active.writer.WriteFrame(frame)
	if !stop() {
		<-canceled
	}
	if operation.Err() != nil {
		err = errors.Join(err, operation.Err())
	} else if err != nil {
		// A socket deadline can fire just before the context timer. Preserve
		// the same cancellation contract in either scheduling order.
		var timeout net.Error
		if errors.As(err, &timeout) && timeout.Timeout() && !time.Now().Before(deadline) {
			err = errors.Join(err, context.DeadlineExceeded)
		}
	}
	if err == nil {
		err = active.session.SetWriteDeadline(time.Time{})
	}
	if err != nil {
		_ = active.close()
	}
	return err
}
