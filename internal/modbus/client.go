// Package modbus implements the subset of Modbus-TCP that the gok agent needs
// to talk to battery systems.
//
// It is deliberately read-only: the only function codes implemented are 0x03
// (read holding registers) and 0x2B/0x0E (read device identification). There is
// no code path anywhere in this package that writes to a device, so a binary
// that links it cannot alter the state of the equipment it is pointed at.
//
// Framing follows the MBAP header of the Modbus-TCP specification: a two-byte
// transaction identifier echoed by the server, a two-byte protocol identifier
// that is always zero, a two-byte length covering everything that follows it,
// and a one-byte unit (logical device) identifier. Unit 0 addresses the
// directly connected node; other units address devices behind it.
package modbus

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"time"
)

const (
	// mbapLen is the fixed size of the MBAP header.
	mbapLen = 7
	// maxADU is the recommended frame length; anything larger is a framing error.
	maxADU = 260

	funcReadHolding  = 0x03
	funcReadDeviceID = 0x2B
	meiDeviceID      = 0x0E

	// exceptionBit marks a response function code as an exception.
	exceptionBit = 0x80
)

// MaxReadCount is the largest number of registers a single 0x03 request may ask
// for.
const MaxReadCount = 125

// Device identification read codes.
const (
	// ReadDevIDBasic returns the mandatory vendor, product code and revision.
	ReadDevIDBasic uint8 = 0x01
	// ReadDevIDList returns the vendor's device list, starting at ObjectDeviceCount.
	ReadDevIDList uint8 = 0x03
)

// Object identifiers of the device identification address space.
const (
	ObjectVendor      uint8 = 0x00
	ObjectProductCode uint8 = 0x01
	ObjectRevision    uint8 = 0x02
	// ObjectDeviceCount holds the number of devices reachable behind this unit;
	// the objects above it carry one description string per device.
	ObjectDeviceCount uint8 = 0x87
)

// Exception is a Modbus exception response. The codes below 0x80 are the
// standard ones; 0x80, 0x90 and 0x91 are defined by Huawei.
type Exception uint8

const (
	ExceptionInvalidFunction   Exception = 0x01
	ExceptionInvalidAddress    Exception = 0x02
	ExceptionInvalidValue      Exception = 0x03
	ExceptionSlaveFailure      Exception = 0x04
	ExceptionSlaveBusy         Exception = 0x06
	ExceptionNoPermission      Exception = 0x80
	ExceptionSouthboundTimeout Exception = 0x90
	ExceptionInternalTimeout   Exception = 0x91
)

// Error implements error.
func (e Exception) Error() string {
	if s, ok := exceptionNames[e]; ok {
		return fmt.Sprintf("modbus exception 0x%02X: %s", uint8(e), s)
	}

	return fmt.Sprintf("modbus exception 0x%02X", uint8(e))
}

// Retryable reports whether the exception describes a transient condition, so
// the same request may reasonably be sent again later.
func (e Exception) Retryable() bool {
	return e == ExceptionSlaveBusy || e == ExceptionSouthboundTimeout || e == ExceptionInternalTimeout
}

var exceptionNames = map[Exception]string{
	ExceptionInvalidFunction:   "invalid function",
	ExceptionInvalidAddress:    "invalid data address",
	ExceptionInvalidValue:      "invalid data value",
	ExceptionSlaveFailure:      "slave node failure",
	ExceptionSlaveBusy:         "slave node busy",
	ExceptionNoPermission:      "no permission",
	ExceptionSouthboundTimeout: "southbound access device response timeout",
	ExceptionInternalTimeout:   "internal unit response timeout",
}

// Client is a Modbus-TCP client holding one connection to one endpoint.
//
// A Client serialises requests: Modbus-TCP allows several outstanding
// transactions, but devices in this class rarely handle them well, and holding
// a single connection with one request in flight is also the politest way to
// share an endpoint with whatever else is already talking to it.
//
// The zero value is not usable; call New.
type Client struct {
	addr    string
	timeout time.Duration

	mu   sync.Mutex
	conn net.Conn
	txn  uint16
}

// New returns a Client for the given host:port. It does not connect; the first
// request does, or call Connect to fail early.
func New(addr string, timeout time.Duration) *Client {
	if timeout <= 0 {
		timeout = 5 * time.Second
	}

	return &Client{addr: addr, timeout: timeout}
}

// Addr returns the endpoint the client talks to.
func (c *Client) Addr() string { return c.addr }

// Connect establishes the connection if it is not already open.
func (c *Client) Connect(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	_, err := c.connect(ctx)

	return err
}

// Close drops the connection. The Client stays usable: a later request dials again.
func (c *Client) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.reset()
}

// connect returns the live connection, dialling if needed. Caller holds c.mu.
func (c *Client) connect(ctx context.Context) (net.Conn, error) {
	if c.conn != nil {
		return c.conn, nil
	}

	d := net.Dialer{Timeout: c.timeout}
	conn, err := d.DialContext(ctx, "tcp", c.addr)
	if err != nil {
		return nil, fmt.Errorf("dial %s: %w", c.addr, err)
	}
	c.conn = conn

	return conn, nil
}

// reset closes and forgets the connection. Caller holds c.mu.
func (c *Client) reset() error {
	if c.conn == nil {
		return nil
	}

	err := c.conn.Close()
	c.conn = nil

	return err
}

// ReadHoldingRegisters reads count registers starting at addr from the given
// unit, returning them in wire order: register addr first, each word already
// converted from its big-endian representation.
func (c *Client) ReadHoldingRegisters(ctx context.Context, unit uint8, addr, count uint16) ([]uint16, error) {
	if count == 0 || count > MaxReadCount {
		return nil, fmt.Errorf("read %d registers at %d: count must be 1..%d", count, addr, MaxReadCount)
	}

	req := []byte{funcReadHolding, byte(addr >> 8), byte(addr), byte(count >> 8), byte(count)}

	resp, err := c.request(ctx, unit, req)
	if err != nil {
		return nil, fmt.Errorf("read %d registers at %d: %w", count, addr, err)
	}

	// Response PDU is the function code, a byte count, then the register data.
	if len(resp) < 2 {
		return nil, fmt.Errorf("read %d registers at %d: short response", count, addr)
	}

	want := int(count) * 2
	if int(resp[1]) != want || len(resp)-2 != want {
		return nil, fmt.Errorf("read %d registers at %d: got %d data bytes, want %d", count, addr, len(resp)-2, want)
	}

	words := make([]uint16, count)
	for i := range words {
		words[i] = binary.BigEndian.Uint16(resp[2+2*i:])
	}

	return words, nil
}

// DeviceObjects is the decoded payload of a device identification response:
// the objects returned, keyed by object ID.
type DeviceObjects map[uint8]string

// ReadDeviceID performs a 0x2B/0x0E read device identification request,
// following the More/Next Object ID continuation until the device says it has
// nothing further. readCode selects the conformity level: ReadDevIDBasic for
// the mandatory vendor and product strings, ReadDevIDList for the vendor's
// device list.
func (c *Client) ReadDeviceID(ctx context.Context, unit, readCode, firstObject uint8) (DeviceObjects, error) {
	out := DeviceObjects{}
	object := firstObject

	// The continuation is bounded by the 256 possible object IDs; the counter
	// stops a device that answers "more follows" forever.
	for range 256 {
		req := []byte{funcReadDeviceID, meiDeviceID, readCode, object}

		resp, err := c.request(ctx, unit, req)
		if err != nil {
			return nil, fmt.Errorf("read device id (code %d, object 0x%02X): %w", readCode, object, err)
		}

		// PDU: func, MEI, read code, conformity, more, next object, count, then objects.
		if len(resp) < 7 {
			return nil, fmt.Errorf("read device id: short response (%d bytes)", len(resp))
		}
		if resp[1] != meiDeviceID {
			return nil, fmt.Errorf("read device id: MEI type 0x%02X, want 0x%02X", resp[1], meiDeviceID)
		}

		more, next, count := resp[4], resp[5], resp[6]

		p := 7
		for range count {
			if p+2 > len(resp) {
				return nil, errors.New("read device id: object list truncated")
			}
			id, n := resp[p], int(resp[p+1])
			p += 2
			if p+n > len(resp) {
				return nil, errors.New("read device id: object value truncated")
			}
			out[id] = string(resp[p : p+n])
			p += n
		}

		if more == 0 {
			return out, nil
		}
		object = next
	}

	return out, nil
}

// request sends one PDU and returns the response PDU, or an Exception if the
// device answered with one. A transport error drops the connection so the next
// call redials rather than reusing a stream of unknown position.
func (c *Client) request(ctx context.Context, unit uint8, pdu []byte) ([]byte, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	conn, err := c.connect(ctx)
	if err != nil {
		return nil, err
	}

	deadline := time.Now().Add(c.timeout)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	if err := conn.SetDeadline(deadline); err != nil {
		_ = c.reset()

		return nil, err
	}

	c.txn++
	txn := c.txn

	frame := make([]byte, mbapLen+len(pdu))
	binary.BigEndian.PutUint16(frame[0:], txn)
	binary.BigEndian.PutUint16(frame[2:], 0)                  // protocol identifier
	binary.BigEndian.PutUint16(frame[4:], uint16(len(pdu)+1)) // unit byte plus PDU
	frame[6] = unit
	copy(frame[mbapLen:], pdu)

	if _, err := conn.Write(frame); err != nil {
		_ = c.reset()

		return nil, fmt.Errorf("write: %w", err)
	}

	resp, err := readFrame(conn, txn, unit)
	if err != nil {
		_ = c.reset()

		return nil, err
	}

	if resp[0]&exceptionBit != 0 {
		if len(resp) < 2 {
			return nil, errors.New("exception response carries no code")
		}

		return nil, Exception(resp[1])
	}

	if resp[0] != pdu[0] {
		return nil, fmt.Errorf("response function code 0x%02X, want 0x%02X", resp[0], pdu[0])
	}

	return resp, nil
}

// readFrame reads one MBAP-framed response and returns its PDU, checking that
// it belongs to the request identified by txn and unit.
func readFrame(conn net.Conn, txn uint16, unit uint8) ([]byte, error) {
	var head [mbapLen]byte
	if _, err := io.ReadFull(conn, head[:]); err != nil {
		return nil, fmt.Errorf("read header: %w", err)
	}

	if got := binary.BigEndian.Uint16(head[0:]); got != txn {
		return nil, fmt.Errorf("response transaction id %d, want %d", got, txn)
	}
	if got := binary.BigEndian.Uint16(head[2:]); got != 0 {
		return nil, fmt.Errorf("response protocol id %d, want 0", got)
	}
	if head[6] != unit {
		return nil, fmt.Errorf("response unit id %d, want %d", head[6], unit)
	}

	length := int(binary.BigEndian.Uint16(head[4:]))
	if length < 2 || length > maxADU {
		return nil, fmt.Errorf("response length %d out of range", length)
	}

	pdu := make([]byte, length-1) // the length covers the unit byte too
	if _, err := io.ReadFull(conn, pdu); err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}

	return pdu, nil
}
