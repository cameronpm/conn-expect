package expect

import (
	"context"
	"errors"
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

// Log is used in combination with the Logger method of the Expect type, to log all inbound/outbound network traffic
type Log struct {
	Type MsgType
	Data string
}

// Batcher is the simple interface all batch records implement
type Batcher interface {
	// Invoke
	//
	// batchIdx is the index of the batch object, starting at 0
	Invoke(ctx context.Context, exp *Expect, timeout time.Duration, batchIdx int) error
}

// BRecv allows regexps to be matched on input
type BRecv struct {
	//  Re is required, it's the regular expression to use for matching
	Re *regexp.Regexp
	// OnSuccess is optional, the first element is the entire matched pattern,
	// the subsequent elements are submatches. Returning an error from here will
	// stop the Batch operation and return with an error
	OnSuccess       func(matched []string) error
	OnSuccessInject func(matched []string) ([]Batcher, error)
}

// Invoke fulfils the Batcher interface
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

	if cbExisted, err := b.onSuccess(exp, offs); cbExisted {
		return err
	}

	exp.buf = exp.buf[offs[1]:]

	return nil
}

func (b *BRecv) onSuccess(exp *Expect, offs []int) (bool, error) {
	if b.OnSuccess != nil {
		amt := make([]string, len(offs)/2)
		for i := 0; i < len(offs); i += 2 {
			amt[i/2] = string(exp.buf[offs[i]:offs[i+1]])
		}
		exp.buf = exp.buf[offs[1]:]
		return true, b.OnSuccess(amt)
	} else if b.OnSuccessInject != nil {
		amt := make([]string, len(offs)/2)
		for i := 0; i < len(offs); i += 2 {
			amt[i/2] = string(exp.buf[offs[i]:offs[i+1]])
		}
		exp.buf = exp.buf[offs[1]:]
		injected, err := b.OnSuccessInject(amt)
		if err == nil {
			exp.injected = injected
		}
		return true, err
	}

	return false, nil
}

// BSwitchOption holds each possibility stored in a BSwitch
type BSwitchOption struct {
	// BRecv
	BRecv
	// Child contains the actions to be run if the Re matches
	Child []Batcher
}

// BSwitch allows regexps to be matched on input, and provide different
// execution paths depending on the matches
type BSwitch []*BSwitchOption

// Invoke fulfils the Batcher interface
func (bs BSwitch) Invoke(ctx context.Context, exp *Expect, timeout time.Duration, batchIdx int) error {
	defer exp.Conn.SetReadDeadline(time.Time{})
	exp.Conn.SetReadDeadline(time.Now().Add(timeout))

	var b *BSwitchOption
	var offs []int
OUTER:
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		for i := range bs {
			offs = bs[i].Re.FindSubmatchIndex(exp.buf)
			if offs != nil {
				b = bs[i]
				break OUTER
			}
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

	if cbExisted, err := b.onSuccess(exp, offs); cbExisted {
		exp.injected = append(exp.injected, b.Child...)
		return err
	}
	exp.injected = b.Child

	exp.buf = exp.buf[offs[1]:]

	return nil

}

// BWipeBuf flushes any cached inbound data. You probably don't want to call this.
type BWipeBuf struct{}

// Invoke fulfils the Batcher interface
func (b *BWipeBuf) Invoke(ctx context.Context, exp *Expect, timeout time.Duration, batchIdx int) error {
	exp.buf = nil
	return nil
}

// BSend sends data through the net.Conn interface
type BSend struct {
	// Data stores the string to be sent over the connection
	Data string
	// OnSuccess is optional, returning an error from here will
	// stop the Batch operation and return with an error
	OnSuccess func() error
}

// Invoke fulfils the Batcher interface
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

// BSendDyn is like BSend however it determines what will be sent by calling the Data() callback
type BSendDyn struct {
	Data      func() string // Data is required, the result is treated like BSend.Data
	OnSuccess func() error  // OnSuccess is like BSend.OnSuccess, it is optional
}

// Invoke fulfils the Batcher interface
func (b *BSendDyn) Invoke(ctx context.Context, exp *Expect, timeout time.Duration, batchIdx int) error {
	bs := BSend{Data: b.Data(), OnSuccess: b.OnSuccess}
	return bs.Invoke(ctx, exp, timeout, batchIdx)
}

// BCallback calls the callback, nothing more. Useful for regression testing.
type BCallback struct {
	Callback func()
}

// Invoke fulfils the Batcher interface
func (b *BCallback) Invoke(ctx context.Context, exp *Expect, timeout time.Duration, batchIdx int) error {
	b.Callback()
	return nil
}

// type Encoding string

// const (
// 	EncUtf8 Encoding = ""
// 	EncNone Encoding = "none"
// )

type Expect struct {
	//Encoding Encoding
	Logger   func(msg Log)
	Conn     net.Conn
	buf      []byte
	injected []Batcher
}

// Batch calls Batcher with a context.Background() for context
func (e *Expect) Batch(timeout time.Duration, batches ...Batcher) error {
	return e.BatchContext(context.Background(), timeout, batches...)
}

// Error is the struct that all errors returned by Batch/BatchContext are wrapped in
type Error struct {
	BatchIdx int
	Orig     error
	Op       Batcher
}

func (err *Error) Error() string {
	return fmt.Sprintf("Batch op with type %T at index %d failed: %s", err.Op, err.BatchIdx, err.Orig)
}

// Support Is(error) and Unwrap() implicit interfaces for errors.Is/As
func (e *Error) Is(err error) bool { return e == err || errors.Is(e.Orig, err) }
func (e *Error) Unwrap() error     { return e.Orig }

// BatchContext allows multiple batched requests/responses. timeout operates on a
// per command basis, to limit the total time that can be used set a deadline on
// the passed in context.
func (e *Expect) BatchContext(ctx context.Context, timeout time.Duration, ops ...Batcher) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() {
		<-ctx.Done()
		e.Conn.SetDeadline(time.Now())
	}()

	return e.batchContext(ctx, timeout, ops)
}

func (e *Expect) batchContext(ctx context.Context, timeout time.Duration, ops []Batcher) error {
	e.injected = nil
	for i, op := range ops {
		if err := op.Invoke(ctx, e, timeout, i); err != nil {
			return &Error{
				BatchIdx: i,
				Op:       op,
				Orig:     err,
			}
		}
		if moreOps := e.injected; moreOps != nil {
			if err := e.batchContext(ctx, timeout, moreOps); err != nil {
				return err
			}
		}
	}

	return nil
}
