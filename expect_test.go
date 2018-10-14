package expect_test

import (
	"context"
	"fmt"
	"io"
	"net"
	"regexp"
	"testing"
	"time"

	expect "github.com/cameronpm/conn-expect"
)

var _ expect.Batcher = (*expect.BRecv)(nil)
var _ expect.Batcher = (*expect.BWipeBuf)(nil)
var _ expect.Batcher = (*expect.BSend)(nil)
var _ expect.Batcher = (*expect.BSendDyn)(nil)
var _ expect.Batcher = (*expect.BCallback)(nil)
var _ expect.Batcher = (*expect.BSwitch)(nil)

func genConn(ctx context.Context, t *testing.T, args ...interface{}) net.Conn {
	l, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal("Unable to open test listen socket", err)
	}

	go func() {
		defer l.Close()
		send := true
		conn, err := l.Accept()
		if err != nil {
			t.Fatal("conn accept failed:", err)
		}
		l.Close()
		defer conn.Close()
		go func() { <-ctx.Done(); conn.Close() }()

		for _, arg := range args {
			switch arg := arg.(type) {
			case string:
				if send {
					if _, err := conn.Write([]byte(arg)); err != nil {
						return
					}
				} else {
					var buf [512]byte
					amt, err := io.ReadFull(conn, buf[:len(arg)])
					if err != nil {
						return
					}
					got := string(buf[:amt])
					if got != arg {
						t.Fatalf("Got ")
					}
				}
				send = !send
			case int:
				select {
				case <-time.After(time.Millisecond * time.Duration(arg)):
				case <-ctx.Done():
					l.Close()
				}
			default:
				t.Fatal("bad regression test")
			}
		}
	}()

	conn, err := net.Dial("tcp4", l.Addr().String())
	if err != nil {
		t.Fatal("Unable to open test listen socket", err)
	}

	return conn
}

func TestExpect_Basic(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	e := &expect.Expect{
		Conn: genConn(ctx, t,
			"Username: ", "admin\r\n",
			"Password: ", "password\r\n",
			"> ", 1000),
	}
	// go func() {
	// 	select {
	// 	case <-ctx.Done():
	// 		return
	// 	case <-time.After(1000 * time.Millisecond):
	// 		pprof.Lookup("goroutine").WriteTo(os.Stdout, 1)
	// 	}
	// }()
	err := e.BatchContext(ctx, 400*time.Millisecond,
		&expect.BRecv{Re: regexp.MustCompile(`Username: `)},
		&expect.BSend{Data: "admin\r\n"},
		&expect.BRecv{Re: regexp.MustCompile(`Password: `)},
		&expect.BSend{Data: "password\r\n"},
		&expect.BRecv{Re: regexp.MustCompile(`> `)},
	)
	if err != nil {
		t.Fatal(err)
	}
}

func TestExpect_MultiRecv(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	e := &expect.Expect{
		Conn: genConn(ctx, t,
			"Username: ", "admin\r\n",
			"Password: ", "password\r\n",
			"> ", "ls\r\n",
			"Not much\r\n> ", 1000),
	}
	err := e.BatchContext(ctx, 400*time.Millisecond,
		&expect.BRecv{Re: regexp.MustCompile(`Username: `)},
		&expect.BSend{Data: "admin\r\n"},
		&expect.BRecv{Re: regexp.MustCompile(`Password: `)},
		&expect.BSend{Data: "password\r\n"},
		&expect.BRecv{Re: regexp.MustCompile(`> `)},
		&expect.BSend{Data: "ls\r\n"},
		&expect.BRecv{Re: regexp.MustCompile(`Not much\r\n`)},
		&expect.BRecv{Re: regexp.MustCompile(`> `)},
	)
	if err != nil {
		t.Fatal(err)
	}
}

func TestExpect_Dynamic(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	e := &expect.Expect{
		Conn: genConn(ctx, t,
			"Username: ", "admin\r\n",
			"Dynamic One Ok", "Matched One Yay\r\n",
			"> ", 1000),
	}
	var match string
	var called bool
	err := e.BatchContext(ctx, 400*time.Millisecond,
		&expect.BRecv{Re: regexp.MustCompile(`Username: `)},
		&expect.BSend{Data: "admin\r\n"},
		&expect.BRecv{
			Re: regexp.MustCompile(`Dynamic (\w+) Ok`),
			OnSuccess: func(matches []string) error {
				match = matches[1]
				return nil
			},
		},
		&expect.BSendDyn{Data: func() string { return fmt.Sprintf("Matched %s Yay\r\n", match) }},
		&expect.BCallback{Callback: func() { called = true }},
		&expect.BRecv{Re: regexp.MustCompile(`> `)},
		&expect.BWipeBuf{},
	)
	if err != nil {
		t.Error("Expect did not complete:", err)
	}
	if !called {
		t.Error("Callback was not called")
	}
}

func TestExpect_Context_Failure(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2000*time.Millisecond)
	defer cancel()
	time.AfterFunc(10*time.Millisecond, cancel)
	e := &expect.Expect{
		Conn: genConn(ctx, t, 1000),
	}
	start := time.Now()
	err := e.BatchContext(ctx, 1500*time.Millisecond,
		&expect.BRecv{Re: regexp.MustCompile(`Username: `)},
	)
	took := time.Now().Sub(start)
	if err == nil {
		t.Error("Did not timeout")
	}
	if took > 500*time.Millisecond {
		t.Errorf("Took too long: %.0fms", float64(took)/float64(time.Millisecond))
	}
	//eerr := err.(*expect.Error).Orig.(*net.OpError)
	//fmt.Printf("%T %+v\n", eerr, eerr)
}

func TestExpect_Dynamic_Failure_Send(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2000*time.Millisecond)
	defer cancel()
	time.AfterFunc(10*time.Millisecond, cancel)
	e := &expect.Expect{
		Conn: genConn(ctx, t, "Server", "Client", "Server", 1000),
	}
	var cancelled bool
	start := time.Now()
	err := e.BatchContext(ctx, 1500*time.Millisecond,
		&expect.BRecv{Re: regexp.MustCompile(`Server`)},
		&expect.BSend{Data: `Client`, OnSuccess: func() error { cancelled = true; cancel(); return nil }},
		&expect.BRecv{Re: regexp.MustCompile(`Server`)},
	)
	took := time.Now().Sub(start)
	if !cancelled {
		t.Error("OnSuccess not called")
	}
	if err == nil {
		t.Error("Did not timeout")
	}
	if took > 500*time.Millisecond {
		t.Errorf("Took too long: %.0fms", float64(took)/float64(time.Millisecond))
	}
}
func TestExpect_Dynamic_Failure_Recv(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2000*time.Millisecond)
	defer cancel()
	time.AfterFunc(10*time.Millisecond, cancel)
	e := &expect.Expect{
		Conn: genConn(ctx, t, "Server", "Client", "Server", 1000),
	}
	var cancelled bool
	start := time.Now()
	err := e.BatchContext(ctx, 1500*time.Millisecond,
		&expect.BRecv{
			Re: regexp.MustCompile(`Server`),
			OnSuccess: func(str []string) error {
				cancelled = true
				cancel()
				return nil
			},
		},
		&expect.BSend{Data: `Client`},
		&expect.BRecv{Re: regexp.MustCompile(`Server`)},
	)
	took := time.Now().Sub(start)
	if !cancelled {
		t.Error("OnSuccess not called")
	}
	if err == nil {
		t.Error("Did not timeout")
	}
	if took > 500*time.Millisecond {
		t.Errorf("Took too long: %.0fms", float64(took)/float64(time.Millisecond))
	}
}

func TestExpect_Logging(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	texts := []interface{}{"Username: ", "admin\r\n", "Password: ", "password\r\n", "> "}
	var msgs []expect.Log
	e := &expect.Expect{
		Conn: genConn(ctx, t, append(texts, 1000)...),
		Logger: func(msg expect.Log) {
			msgs = append(msgs, msg)
		},
	}
	err := e.BatchContext(ctx, 400*time.Millisecond,
		&expect.BRecv{Re: regexp.MustCompile(`Username: `)},
		&expect.BSend{Data: "admin\r\n"},
		&expect.BRecv{Re: regexp.MustCompile(`Password: `)},
		&expect.BSend{Data: "password\r\n"},
		&expect.BRecv{Re: regexp.MustCompile(`> `)},
	)
	if err != nil {
		t.Error("Polling failed with error:", err)
	}
	if len(msgs) != len(texts) {
		t.Errorf("Expected %d log messages, got %d", len(texts), len(msgs))
	}
	mtype := []expect.MsgType{expect.MsgRecv, expect.MsgSend}
	for i := range msgs {
		if mtype[i%2] != msgs[i].Type {
			t.Errorf("Incorrect message type")
		}
		if msgs[i].Data != texts[i].(string) {
			t.Errorf("Incorrect data type")
		}
	}
}

func bsend(data string, success func() error) *expect.BSend {
	return &expect.BSend{Data: data, OnSuccess: success}
}
func brecv(re string, success func([]string) error) *expect.BRecv {
	return &expect.BRecv{
		Re:        regexp.MustCompile(re),
		OnSuccess: success,
	}
}
func bswitch(opts ...*expect.BSwitchOption) expect.BSwitch {
	return expect.BSwitch(opts)
}
func bswitchopt(re string, success func([]string) error, batched ...expect.Batcher) *expect.BSwitchOption {
	return &expect.BSwitchOption{
		BRecv: expect.BRecv{
			Re:        regexp.MustCompile(re),
			OnSuccess: success,
		},
		Child: batched,
	}
}

func TestExpect_Switch(t *testing.T) {
	allArgs := []struct {
		desc string
		args []interface{}
	}{
		{"Enable with password", []interface{}{
			"Username: ", "admin\r\n",
			">", "enable\r\n",
			"Password: ", "password\r\n",
			"#", 1000,
		}},
		{"Enable without password", []interface{}{
			"Username: ", "admin\r\n",
			">", "enable\r\n",
			"#", 1000,
		}},
		{"Already enabled", []interface{}{
			"Username: ", "admin\r\n",
			"#", 1000,
		}},
	}

	var called bool
	opts := []expect.Batcher{
		brecv(`Username: `, nil),
		bsend("admin\r\n", nil),
		bswitch(
			bswitchopt(`>`, nil,
				bsend("enable\r\n", nil),
				bswitch(
					bswitchopt(`Password: `, nil,
						bsend("password\r\n", nil),
						brecv(`#`, nil)),
					bswitchopt(`#`, nil)),
			),
			bswitchopt(`#`, nil)),
		&expect.BCallback{Callback: func() { called = true }},
	}

	for _, args := range allArgs {
		ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		t.Logf("# Subtest '%s'", args.desc)
		called = false
		e := &expect.Expect{
			Conn: genConn(ctx, t, args.args...),
			Logger: func(msg expect.Log) {
				t.Logf("  [%s] %q\n", msg.Type, msg.Data)
			},
		}
		err := e.BatchContext(ctx, 400*time.Millisecond, opts...)
		if err != nil {
			t.Fatal("Expect did not complete:", err)
		}
		if !called {
			t.Fatal("Callback was not called")
		}
		cancel()
	}
}
