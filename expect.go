package expect

import (
	"context"
	"fmt"
	"net"
	"regexp"
	"time"
)

type MsgType string

const (
	MsgRecv MsgType = "recv"
	MsgSend MsgType = "send"
)

type Log struct {
	Type MsgType
	Data string
}

type Batcher interface {
	// Invoke
	//
	// batchIdx is the index of the batch object, starting at 0
	Invoke(ctx context.Context, exp *Expect, timeout time.Duration, batchIdx int) error
}

type BRecv struct {
	OnSuccess func(matched []string) error
	Re        *regexp.Regexp
}

func (b *BRecv) Invoke(ctx context.Context, exp *Expect, timeout time.Duration, batchIdx int) error {
	defer exp.Conn.SetReadDeadline(time.Time{})
	exp.Conn.SetReadDeadline(time.Now().Add(timeout))

	var offs []int
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		offs = b.Re.FindSubmatchIndex(exp.buf)
		if offs != nil {
			break
		}

		var buf [512]byte
		sz, err := exp.Conn.Read(buf[:])
		if err != nil {
			return err
		}

		if exp.Logger != nil {
			exp.Logger(Log{Type: MsgRecv, Data: string(buf[:sz])})
		}
		exp.buf = append(exp.buf, buf[:sz]...)
	}

	if b.OnSuccess != nil {
		amt := make([]string, len(offs)/2)
		for i := 0; i < len(offs); i += 2 {
			amt[i/2] = string(exp.buf[offs[i]:offs[i+1]])
		}
		exp.buf = exp.buf[offs[1]:]
		return b.OnSuccess(amt)
	}

	exp.buf = exp.buf[offs[1]:]

	return nil
}

type BWipeBuf struct{}

func (b *BWipeBuf) Invoke(ctx context.Context, exp *Expect, timeout time.Duration, batchIdx int) error {
	exp.buf = nil
	return nil
}

type BSend struct {
	Data      string
	OnSuccess func() error
}

func (b *BSend) Invoke(ctx context.Context, exp *Expect, timeout time.Duration, batchIdx int) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	defer exp.Conn.SetWriteDeadline(time.Time{})
	exp.Conn.SetWriteDeadline(time.Now().Add(timeout))

	if _, err := exp.Conn.Write([]byte(b.Data)); err != nil {
		return err
	}
	if exp.Logger != nil {
		exp.Logger(Log{Type: MsgSend, Data: b.Data})
	}
	if b.OnSuccess != nil {
		return b.OnSuccess()
	}
	return nil
}

type BSendDyn struct {
	Data      func() string
	OnSuccess func() error
}

func (b *BSendDyn) Invoke(ctx context.Context, exp *Expect, timeout time.Duration, batchIdx int) error {
	bs := BSend{Data: b.Data(), OnSuccess: b.OnSuccess}
	return bs.Invoke(ctx, exp, timeout, batchIdx)
}

type BCallback struct {
	Callback func()
}

func (b *BCallback) Invoke(ctx context.Context, exp *Expect, timeout time.Duration, batchIdx int) error {
	b.Callback()
	return nil
}

type Encoding string

const (
	EncUtf8 Encoding = ""
	EncNone Encoding = "none"
)

type Expect struct {
	Encoding Encoding
	Logger   func(msg Log)
	Conn     net.Conn
	buf      []byte
}

func (e *Expect) Batch(timeout time.Duration, batches ...Batcher) error {
	return e.BatchContext(context.Background(), timeout, batches...)
}

type Error struct {
	BatchIdx int
	Orig     error
	Op       Batcher
}

func (err *Error) Error() string {
	return fmt.Sprintf("Batch op with type %T at index %d failed: %s", err.Op, err.BatchIdx, err.Orig)
}

func (e *Expect) BatchContext(ctx context.Context, timeout time.Duration, ops ...Batcher) error {
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-done:
			return
		case <-ctx.Done():
			e.Conn.SetDeadline(time.Now())
		}
	}()

	for i, op := range ops {
		if err := op.Invoke(ctx, e, timeout, i); err != nil {
			return &Error{
				BatchIdx: i,
				Op:       op,
				Orig:     err,
			}
		}
	}

	return nil
}
