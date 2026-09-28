package modbus

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
)

const (
	funcWriteSingle   = 0x06
	funcWriteMultiple = 0x10
)

// MaxWriteCount is the largest number of registers a single 0x10 request may
// carry.
const MaxWriteCount = 123

// Writer writes holding registers through a Client's connection.
//
// It is a separate type so that reading never implies the ability to write: a
// Client has no write methods, and a binary can alter equipment state only if
// it calls NewWriter. That is a weaker property than the package once had (no
// write code at all), and it is checked by looking for call sites.
//
// Writes are never retried here. If the transport fails after the request was
// sent, the device may or may not have applied it; read the register back to
// find out.
type Writer struct {
	c *Client
}

// NewWriter returns a Writer that sends through c.
func NewWriter(c *Client) *Writer { return &Writer{c: c} }

// WriteSingleRegister writes value to addr with function code 0x06. The device
// must echo the request; any other response is an error.
func (w *Writer) WriteSingleRegister(ctx context.Context, unit uint8, addr, value uint16) error {
	req := []byte{funcWriteSingle, byte(addr >> 8), byte(addr), byte(value >> 8), byte(value)}

	resp, err := w.c.request(ctx, unit, req)
	if err != nil {
		return fmt.Errorf("write register %d (0x06): %w", addr, err)
	}
	if !bytes.Equal(resp, req) {
		return fmt.Errorf("write register %d (0x06): response % X does not echo the request", addr, resp)
	}

	return nil
}

// WriteMultipleRegisters writes values to consecutive registers starting at
// addr with function code 0x10. The device must answer with the same start
// address and quantity.
func (w *Writer) WriteMultipleRegisters(ctx context.Context, unit uint8, addr uint16, values []uint16) error {
	n := len(values)
	if n == 0 || n > MaxWriteCount {
		return fmt.Errorf("write %d registers at %d: count must be 1..%d", n, addr, MaxWriteCount)
	}

	req := make([]byte, 6, 6+2*n)
	req[0] = funcWriteMultiple
	binary.BigEndian.PutUint16(req[1:], addr)
	binary.BigEndian.PutUint16(req[3:], uint16(n))
	req[5] = byte(2 * n)
	for _, v := range values {
		req = binary.BigEndian.AppendUint16(req, v)
	}

	resp, err := w.c.request(ctx, unit, req)
	if err != nil {
		return fmt.Errorf("write %d registers at %d (0x10): %w", n, addr, err)
	}
	if len(resp) != 5 || !bytes.Equal(resp[1:5], req[1:5]) {
		return fmt.Errorf("write %d registers at %d (0x10): response % X does not match the request", n, addr, resp)
	}

	return nil
}
