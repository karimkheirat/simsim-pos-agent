package scale

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/karimkheirat/simsim-pos-agent/internal/scale/link32"
)

// fakeScale is an in-process TCP peer speaking just enough link32 to
// exercise the client: it reads frames and lets the test script the
// per-command response.
type fakeScale struct {
	ln net.Listener
	// respond maps a received command to the raw bytes to write back.
	// A missing key means "no response" (the client should time out if
	// it expects one). Called per frame via the respond func.
	respond func(cmd string, record []byte) []byte
	// received collects (command, record) pairs across the session.
	gotCh chan string
}

func newFakeScale(t *testing.T, respond func(cmd string, record []byte) []byte) *fakeScale {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	f := &fakeScale{ln: ln, respond: respond, gotCh: make(chan string, 64)}
	go f.serve()
	t.Cleanup(func() { _ = ln.Close() })
	return f
}

func (f *fakeScale) serve() {
	for {
		conn, err := f.ln.Accept()
		if err != nil {
			return
		}
		go func(c net.Conn) {
			defer c.Close()
			for {
				cmd, record, err := link32.ReadFrame(c)
				if err != nil {
					return
				}
				f.gotCh <- cmd
				if resp := f.respond(cmd, record); resp != nil {
					if _, err := c.Write(resp); err != nil {
						return
					}
				}
			}
		}(conn)
	}
}

func (f *fakeScale) addr() (string, int) {
	tcpAddr := f.ln.Addr().(*net.TCPAddr)
	return tcpAddr.IP.String(), tcpAddr.Port
}

// ackOK builds a well-formed 0202 ACK for the given command.
func ackOK(cmd string) []byte {
	frame, err := link32.Frame(link32.CmdAck, []byte(cmd+"000000"+"0000"))
	if err != nil {
		panic(err)
	}
	return frame
}

// ackErr builds a 0202 ACK carrying a nonzero error code.
func ackErr(cmd, code string) []byte {
	frame, err := link32.Frame(link32.CmdAck, []byte(cmd+"000000"+code))
	if err != nil {
		panic(err)
	}
	return frame
}

func weighedEntry(plu string) PLU {
	return PLU{PLU: plu, Name: "Tomates", PriceCentimes: 25050, SoldBy: "weight", MeasureUnit: "kg"}
}

// ── Happy path ────────────────────────────────────────────────────────

func TestSendPLUs_HappyPath(t *testing.T) {
	f := newFakeScale(t, func(cmd string, _ []byte) []byte {
		if cmd == link32.CmdPLURecord {
			return ackOK(cmd)
		}
		return nil // no ACK to the 0201 start command, per the flowchart
	})
	ip, port := f.addr()
	s := NewTCP(ip, port)

	entries := []PLU{weighedEntry("101"), {PLU: "202", Name: "Pain", PriceCentimes: 3000, SoldBy: "piece", MeasureUnit: "kg"}}
	results, err := s.SendPLUs(context.Background(), entries)
	if err != nil {
		t.Fatalf("SendPLUs: %v", err)
	}
	for i, r := range results {
		if !r.OK || r.Error != "" {
			t.Errorf("results[%d] = %+v, want OK", i, r)
		}
	}

	// Wire order: 0201 start first, then one 0110 per PLU.
	wantCmds := []string{link32.CmdStart, link32.CmdPLURecord, link32.CmdPLURecord}
	for i, want := range wantCmds {
		select {
		case got := <-f.gotCh:
			if got != want {
				t.Errorf("frame[%d] command = %s, want %s", i, got, want)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("frame[%d]: fake scale saw nothing", i)
		}
	}
}

// ── Per-PLU failures keep the session alive ──────────────────────────

func TestSendPLUs_ScaleErrorCode_IsPerPLUFailure(t *testing.T) {
	var n int
	f := newFakeScale(t, func(cmd string, _ []byte) []byte {
		if cmd != link32.CmdPLURecord {
			return nil
		}
		n++
		if n == 1 {
			return ackErr(cmd, "0007")
		}
		return ackOK(cmd)
	})
	ip, port := f.addr()
	s := NewTCP(ip, port)

	results, err := s.SendPLUs(context.Background(), []PLU{weighedEntry("101"), weighedEntry("102")})
	if err != nil {
		t.Fatalf("SendPLUs: %v", err)
	}
	if results[0].OK || !strings.Contains(results[0].Error, "0007") {
		t.Errorf("results[0] = %+v, want scale error code 0007", results[0])
	}
	if !results[1].OK {
		t.Errorf("results[1] = %+v, want OK (session must survive a rejected PLU)", results[1])
	}
}

func TestSendPLUs_EncodeFailure_NeverTouchesWire(t *testing.T) {
	f := newFakeScale(t, func(cmd string, _ []byte) []byte {
		if cmd == link32.CmdPLURecord {
			return ackOK(cmd)
		}
		return nil
	})
	ip, port := f.addr()
	s := NewTCP(ip, port)

	entries := []PLU{
		{PLU: "abc", Name: "Bad code", PriceCentimes: 100, SoldBy: "weight", MeasureUnit: "kg"},
		{PLU: "77", Name: "Huile", PriceCentimes: 100, SoldBy: "weight", MeasureUnit: "l"},
		weighedEntry("55"),
	}
	results, err := s.SendPLUs(context.Background(), entries)
	if err != nil {
		t.Fatalf("SendPLUs: %v", err)
	}
	if results[0].OK || !strings.Contains(results[0].Error, "ASCII digits") {
		t.Errorf("results[0] = %+v, want digit-validation encode failure", results[0])
	}
	if results[1].OK || !strings.Contains(results[1].Error, "not representable") {
		t.Errorf("results[1] = %+v, want unsupported-unit encode failure", results[1])
	}
	if !results[2].OK {
		t.Errorf("results[2] = %+v, want OK", results[2])
	}

	// Exactly one 0110 frame (for the valid PLU) after the start frame.
	var plus int
	timeout := time.After(2 * time.Second)
	for i := 0; i < 2; i++ {
		select {
		case cmd := <-f.gotCh:
			if cmd == link32.CmdPLURecord {
				plus++
			}
		case <-timeout:
			t.Fatal("fake scale saw too few frames")
		}
	}
	if plus != 1 {
		t.Errorf("scale received %d PLU frames, want 1", plus)
	}
}

func TestSendPLUs_AllEncodeFailures_NoDial(t *testing.T) {
	// Point at a port with no listener: if the client dials, it errors;
	// all-encode-failure input must return without a session error.
	s := NewTCP("127.0.0.1", freePort(t))
	results, err := s.SendPLUs(context.Background(), []PLU{
		{PLU: "", Name: "x", PriceCentimes: 1, SoldBy: "weight", MeasureUnit: "kg"},
	})
	if err != nil {
		t.Fatalf("SendPLUs dialed despite nothing sendable: %v", err)
	}
	if results[0].OK || results[0].Error == "" {
		t.Errorf("results[0] = %+v, want encode failure", results[0])
	}
}

// ── Session failures ──────────────────────────────────────────────────

func TestSendPLUs_DialFailure(t *testing.T) {
	s := NewTCP("127.0.0.1", freePort(t))
	results, err := s.SendPLUs(context.Background(), []PLU{weighedEntry("101")})
	if err == nil {
		t.Fatal("SendPLUs: want dial error")
	}
	if results[0].OK || !strings.Contains(results[0].Error, "not attempted") {
		t.Errorf("results[0] = %+v, want not-attempted marker", results[0])
	}
}

func TestSendPLUs_NoAck_TimesOutAndMarksRest(t *testing.T) {
	f := newFakeScale(t, func(string, []byte) []byte { return nil }) // never ACKs
	ip, port := f.addr()
	s := NewTCP(ip, port)
	s.ioTimeout = 150 * time.Millisecond

	results, err := s.SendPLUs(context.Background(), []PLU{weighedEntry("101"), weighedEntry("102")})
	if err == nil {
		t.Fatal("SendPLUs: want timeout error")
	}
	if results[0].OK || !strings.Contains(results[0].Error, "ack read") {
		t.Errorf("results[0] = %+v, want ack-read failure", results[0])
	}
	if results[1].OK || !strings.Contains(results[1].Error, "not attempted") {
		t.Errorf("results[1] = %+v, want not-attempted marker", results[1])
	}
}

func TestSendPLUs_ContextCanceled(t *testing.T) {
	f := newFakeScale(t, func(string, []byte) []byte { return nil })
	ip, port := f.addr()
	s := NewTCP(ip, port)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := s.SendPLUs(ctx, []PLU{weighedEntry("101")})
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
}

// ── Reachability + naming ─────────────────────────────────────────────

func TestIsReachable(t *testing.T) {
	f := newFakeScale(t, func(string, []byte) []byte { return nil })
	ip, port := f.addr()
	if !NewTCP(ip, port).IsReachable() {
		t.Error("IsReachable = false against a live listener")
	}
	if NewTCP("127.0.0.1", freePort(t)).IsReachable() {
		t.Error("IsReachable = true against a closed port")
	}
}

func TestName(t *testing.T) {
	if got := NewTCP("192.168.1.50", 5002).Name(); got != "scale:192.168.1.50:5002" {
		t.Errorf("Name = %q", got)
	}
}

// freePort grabs a port that is free at call time (listener closed
// immediately, so a subsequent dial refuses).
func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()
	return port
}
