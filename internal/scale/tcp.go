package scale

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"time"

	"github.com/karimkheirat/simsim-pos-agent/internal/scale/link32"
)

// Timeouts. Reachability matches the printer.Printer contract (~200ms);
// dial/IO are generous enough for a busy scale on store Wi-Fi but small
// enough that a dead scale fails a sync in seconds, not minutes.
const (
	reachProbeTimeout  = 200 * time.Millisecond
	defaultDialTimeout = 3 * time.Second
	defaultIOTimeout   = 5 * time.Second
)

// TCP drives an Aclas LS2-series scale over a store-LAN TCP connection
// using the link32 protocol frames.
//
// Session shape (adapted from the manual's §9.2 flowchart — the
// documented exchange is Link32↔background; the agent plays the
// PLU-sending side against the scale):
//
//  1. Dial <ip>:<port>.
//  2. Send the starting command (0201). The flowchart shows no ACK to
//     the starting command, so none is awaited.
//  3. Per PLU: send a 0110 frame, then read exactly one ACK frame
//     (0202/0102, record = command+LFCode+error code); error code
//     "0000" marks the PLU accepted.
//  4. Close.
//
// TODO(verify-on-hardware): the §9.2 flowchart has Link32 dialing the
// background and RECEIVING PLUs; here the agent dials the scale and
// SENDS them. Whether the scale firmware speaks this exact role
// mirror (and answers 0201/0110 as modeled) is the single biggest
// hardware-verification item for the whole feature.
type TCP struct {
	addr        string
	dialTimeout time.Duration
	ioTimeout   time.Duration
}

// NewTCP returns a TCP scale bound to ip:port. The constructor does not
// touch the network; reachability is observed lazily via IsReachable /
// SendPLUs (same shape as printer.NewWindowsSpooler).
func NewTCP(ip string, port int) *TCP {
	return &TCP{
		addr:        net.JoinHostPort(ip, strconv.Itoa(port)),
		dialTimeout: defaultDialTimeout,
		ioTimeout:   defaultIOTimeout,
	}
}

// Name returns "scale:<ip>:<port>".
func (t *TCP) Name() string { return "scale:" + t.addr }

// IsReachable dials the scale with a 200ms cap and closes immediately.
func (t *TCP) IsReachable() bool {
	conn, err := net.DialTimeout("tcp", t.addr, reachProbeTimeout)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

// SendPLUs implements Scale. See the interface doc for the Result vs
// error split; unattempted entries (after a session failure) carry
// Error "not attempted: ..." so callers can retry precisely.
func (t *TCP) SendPLUs(ctx context.Context, entries []PLU) ([]Result, error) {
	results := make([]Result, len(entries))

	// Encode everything first: encoding failures are per-PLU results,
	// and we only open a connection if at least one frame is sendable.
	frames := make([][]byte, len(entries))
	sendable := 0
	for i, e := range entries {
		results[i] = Result{PLU: e.PLU}
		rec, err := toLink32(e)
		if err == nil {
			var ferr error
			frames[i], ferr = link32.PLUFrame(rec)
			err = ferr
		}
		if err != nil {
			results[i].Error = "encode: " + err.Error()
			continue
		}
		sendable++
	}
	if sendable == 0 {
		return results, nil
	}

	dialer := net.Dialer{Timeout: t.dialTimeout}
	conn, err := dialer.DialContext(ctx, "tcp", t.addr)
	if err != nil {
		markNotAttempted(results, frames, 0)
		return results, fmt.Errorf("scale: dial %s: %w", t.addr, err)
	}
	defer conn.Close()

	if err := t.write(ctx, conn, link32.StartFrame()); err != nil {
		markNotAttempted(results, frames, 0)
		return results, fmt.Errorf("scale: send start command: %w", err)
	}

	for i := range entries {
		if frames[i] == nil {
			continue // per-PLU encode failure already recorded
		}
		if err := t.sendOne(ctx, conn, frames[i], &results[i]); err != nil {
			// Transport/protocol breakdown — the session is dead.
			// Everything not yet attempted is marked and the session
			// error surfaces to the caller.
			markNotAttempted(results, frames, i+1)
			return results, fmt.Errorf("scale: plu %s: %w", entries[i].PLU, err)
		}
	}
	return results, nil
}

// sendOne writes one 0110 frame and consumes its ACK. Scale-side
// rejections (nonzero error code) land in res; the returned error is
// reserved for transport/protocol failures that kill the session.
func (t *TCP) sendOne(ctx context.Context, conn net.Conn, frame []byte, res *Result) error {
	if err := t.write(ctx, conn, frame); err != nil {
		res.Error = "send: " + err.Error()
		return err
	}
	if err := t.setDeadline(ctx, conn); err != nil {
		res.Error = err.Error()
		return err
	}
	cmd, record, err := link32.ReadFrame(conn)
	if err != nil {
		res.Error = "ack read: " + err.Error()
		return err
	}
	// The PLU receiver ACKs with 0202 in the flowchart; 0102 is the
	// same record shape from the background side. Accept either.
	if cmd != link32.CmdAck && cmd != link32.CmdBackgroundAck {
		res.Error = fmt.Sprintf("unexpected response command %q", cmd)
		return fmt.Errorf("unexpected response command %q", cmd)
	}
	ack, err := link32.ParseAck(record)
	if err != nil {
		res.Error = "ack parse: " + err.Error()
		return err
	}
	if !ack.OK() {
		// Documented per-record rejection — session continues.
		res.Error = "scale error code " + ack.ErrorCode
		return nil
	}
	res.OK = true
	res.Error = ""
	return nil
}

// write sends buf under the session deadline.
func (t *TCP) write(ctx context.Context, conn net.Conn, buf []byte) error {
	if err := t.setDeadline(ctx, conn); err != nil {
		return err
	}
	_, err := conn.Write(buf)
	return err
}

// setDeadline applies the per-operation IO timeout, tightened by the
// context deadline when that is sooner. A canceled context surfaces
// as an immediate error.
func (t *TCP) setDeadline(ctx context.Context, conn net.Conn) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	deadline := time.Now().Add(t.ioTimeout)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	return conn.SetDeadline(deadline)
}

// markNotAttempted stamps every sendable-but-unsent entry from index
// `from` onward so callers can distinguish "scale rejected" from
// "never reached the wire".
func markNotAttempted(results []Result, frames [][]byte, from int) {
	for i := from; i < len(results); i++ {
		if frames[i] != nil && !results[i].OK && results[i].Error == "" {
			results[i].Error = "not attempted: session aborted"
		}
	}
}

// Compile-time assertion that *TCP satisfies Scale.
var _ Scale = (*TCP)(nil)
