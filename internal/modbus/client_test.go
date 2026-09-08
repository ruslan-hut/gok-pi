package modbus

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"testing"
	"time"
)

// fakeServer answers Modbus-TCP frames with whatever handle returns for a PDU.
// A nil PDU from handle means "say nothing", which exercises timeouts.
type fakeServer struct {
	t        *testing.T
	ln       net.Listener
	handle   func(unit uint8, pdu []byte) []byte
	requests int
}

func newFakeServer(t *testing.T, handle func(unit uint8, pdu []byte) []byte) *fakeServer {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	s := &fakeServer{t: t, ln: ln, handle: handle}
	go s.serve()
	t.Cleanup(func() { _ = ln.Close() })

	return s
}

func (s *fakeServer) addr() string { return s.ln.Addr().String() }

func (s *fakeServer) serve() {
	for {
		conn, err := s.ln.Accept()
		if err != nil {
			return
		}
		go s.handleConn(conn)
	}
}

func (s *fakeServer) handleConn(conn net.Conn) {
	defer func() { _ = conn.Close() }()

	for {
		var head [mbapLen]byte
		if _, err := io.ReadFull(conn, head[:]); err != nil {
			return
		}
		length := int(binary.BigEndian.Uint16(head[4:]))
		pdu := make([]byte, length-1)
		if _, err := io.ReadFull(conn, pdu); err != nil {
			return
		}
		s.requests++

		resp := s.handle(head[6], pdu)
		if resp == nil {
			continue
		}

		out := make([]byte, mbapLen+len(resp))
		copy(out, head[:])
		binary.BigEndian.PutUint16(out[4:], uint16(len(resp)+1))
		copy(out[mbapLen:], resp)
		if _, err := conn.Write(out); err != nil {
			return
		}
	}
}

// registerServer answers 0x03 out of a register map.
func registerServer(t *testing.T, regs map[uint16]uint16) *fakeServer {
	return newFakeServer(t, func(_ uint8, pdu []byte) []byte {
		if pdu[0] != funcReadHolding {
			return []byte{pdu[0] | exceptionBit, byte(ExceptionInvalidFunction)}
		}

		addr := binary.BigEndian.Uint16(pdu[1:])
		count := binary.BigEndian.Uint16(pdu[3:])

		out := []byte{funcReadHolding, byte(count * 2)}
		for i := range count {
			v, ok := regs[addr+i]
			if !ok {
				return []byte{funcReadHolding | exceptionBit, byte(ExceptionInvalidAddress)}
			}
			out = append(out, byte(v>>8), byte(v))
		}

		return out
	})
}

func TestReadHoldingRegisters(t *testing.T) {
	s := registerServer(t, map[uint16]uint16{
		30400: 85,
		30236: 0x0003, 30237: 0x47D8,
	})
	c := New(s.addr(), time.Second)
	t.Cleanup(func() { _ = c.Close() })

	got, err := c.ReadHoldingRegisters(context.Background(), 0, 30400, 1)
	if err != nil {
		t.Fatalf("ReadHoldingRegisters() error = %v", err)
	}
	if len(got) != 1 || got[0] != 85 {
		t.Errorf("ReadHoldingRegisters() = %v, want [85]", got)
	}

	got, err = c.ReadHoldingRegisters(context.Background(), 0, 30236, 2)
	if err != nil {
		t.Fatalf("ReadHoldingRegisters() error = %v", err)
	}
	if len(got) != 2 || got[0] != 0x0003 || got[1] != 0x47D8 {
		t.Errorf("ReadHoldingRegisters() = %v, want [3 18392]", got)
	}
}

func TestReadHoldingRegistersReusesOneConnection(t *testing.T) {
	s := registerServer(t, map[uint16]uint16{30400: 50})
	c := New(s.addr(), time.Second)
	t.Cleanup(func() { _ = c.Close() })

	for range 5 {
		if _, err := c.ReadHoldingRegisters(context.Background(), 0, 30400, 1); err != nil {
			t.Fatalf("ReadHoldingRegisters() error = %v", err)
		}
	}
	if s.requests != 5 {
		t.Errorf("server saw %d requests, want 5", s.requests)
	}
}

func TestReadHoldingRegistersRejectsBadCount(t *testing.T) {
	c := New("127.0.0.1:1", time.Second)

	if _, err := c.ReadHoldingRegisters(context.Background(), 0, 30400, 0); err == nil {
		t.Error("count 0: expected error")
	}
	if _, err := c.ReadHoldingRegisters(context.Background(), 0, 30400, MaxReadCount+1); err == nil {
		t.Error("count above the limit: expected error")
	}
}

func TestException(t *testing.T) {
	s := newFakeServer(t, func(_ uint8, pdu []byte) []byte {
		return []byte{pdu[0] | exceptionBit, byte(ExceptionNoPermission)}
	})
	c := New(s.addr(), time.Second)
	t.Cleanup(func() { _ = c.Close() })

	_, err := c.ReadHoldingRegisters(context.Background(), 0, 42002, 1)

	var exc Exception
	if !errors.As(err, &exc) {
		t.Fatalf("error = %v, want an Exception", err)
	}
	if exc != ExceptionNoPermission {
		t.Errorf("exception = 0x%02X, want 0x80", uint8(exc))
	}
	if exc.Retryable() {
		t.Error("ExceptionNoPermission.Retryable() = true")
	}
	if !ExceptionSlaveBusy.Retryable() {
		t.Error("ExceptionSlaveBusy.Retryable() = false")
	}
}

func TestRejectsMismatchedResponse(t *testing.T) {
	tests := []struct {
		name   string
		mangle func(head []byte)
	}{
		{"transaction id", func(h []byte) { binary.BigEndian.PutUint16(h[0:], 999) }},
		{"protocol id", func(h []byte) { binary.BigEndian.PutUint16(h[2:], 1) }},
		{"unit id", func(h []byte) { h[6] = 9 }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ln, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatalf("listen: %v", err)
			}
			t.Cleanup(func() { _ = ln.Close() })

			go func() {
				conn, err := ln.Accept()
				if err != nil {
					return
				}
				defer func() { _ = conn.Close() }()

				var head [mbapLen]byte
				if _, err := io.ReadFull(conn, head[:]); err != nil {
					return
				}
				length := int(binary.BigEndian.Uint16(head[4:]))
				if _, err := io.ReadFull(conn, make([]byte, length-1)); err != nil {
					return
				}

				tt.mangle(head[:])
				binary.BigEndian.PutUint16(head[4:], 4)
				_, _ = conn.Write(append(head[:], funcReadHolding, 2, 0, 85))
			}()

			c := New(ln.Addr().String(), time.Second)
			t.Cleanup(func() { _ = c.Close() })

			if _, err := c.ReadHoldingRegisters(context.Background(), 0, 30400, 1); err == nil {
				t.Error("expected error")
			}
		})
	}
}

func TestShortDataRejected(t *testing.T) {
	s := newFakeServer(t, func(_ uint8, _ []byte) []byte {
		// Claims four data bytes but carries two.
		return []byte{funcReadHolding, 4, 0, 85}
	})
	c := New(s.addr(), time.Second)
	t.Cleanup(func() { _ = c.Close() })

	if _, err := c.ReadHoldingRegisters(context.Background(), 0, 30400, 2); err == nil {
		t.Error("expected error")
	}
}

func TestTimeoutDropsConnection(t *testing.T) {
	s := newFakeServer(t, func(_ uint8, _ []byte) []byte { return nil })
	c := New(s.addr(), 100*time.Millisecond)
	t.Cleanup(func() { _ = c.Close() })

	if _, err := c.ReadHoldingRegisters(context.Background(), 0, 30400, 1); err == nil {
		t.Fatal("expected a timeout")
	}

	c.mu.Lock()
	open := c.conn != nil
	c.mu.Unlock()
	if open {
		t.Error("connection kept after a transport error")
	}
}

func TestReadDeviceIDBasic(t *testing.T) {
	s := newFakeServer(t, func(_ uint8, pdu []byte) []byte {
		if pdu[0] != funcReadDeviceID || pdu[1] != meiDeviceID {
			return []byte{pdu[0] | exceptionBit, byte(ExceptionInvalidFunction)}
		}

		objs := [][2]string{{"\x00", "Huawei"}, {"\x01", "LUNA2000B"}, {"\x02", "V200R024C00"}}
		out := []byte{funcReadDeviceID, meiDeviceID, ReadDevIDBasic, 0x01, 0x00, 0x00, byte(len(objs))}
		for i, o := range objs {
			out = append(out, byte(i), byte(len(o[1])))
			out = append(out, o[1]...)
		}

		return out
	})
	c := New(s.addr(), time.Second)
	t.Cleanup(func() { _ = c.Close() })

	got, err := c.ReadDeviceID(context.Background(), 0, ReadDevIDBasic, ObjectVendor)
	if err != nil {
		t.Fatalf("ReadDeviceID() error = %v", err)
	}
	if got[ObjectVendor] != "Huawei" || got[ObjectProductCode] != "LUNA2000B" || got[ObjectRevision] != "V200R024C00" {
		t.Errorf("ReadDeviceID() = %v", got)
	}
}

func TestReadDeviceIDFollowsContinuation(t *testing.T) {
	// First response sets More and points at object 2; the second finishes.
	page := 0
	s := newFakeServer(t, func(_ uint8, _ []byte) []byte {
		page++
		if page == 1 {
			out := []byte{funcReadDeviceID, meiDeviceID, ReadDevIDList, 0x03, 0xFF, 0x02, 0x01, 0x01, 1}

			return append(out, 'a')
		}
		out := []byte{funcReadDeviceID, meiDeviceID, ReadDevIDList, 0x03, 0x00, 0x00, 0x01, 0x02, 1}

		return append(out, 'b')
	})
	c := New(s.addr(), time.Second)
	t.Cleanup(func() { _ = c.Close() })

	got, err := c.ReadDeviceID(context.Background(), 0, ReadDevIDList, 0x01)
	if err != nil {
		t.Fatalf("ReadDeviceID() error = %v", err)
	}
	if len(got) != 2 || got[1] != "a" || got[2] != "b" {
		t.Errorf("ReadDeviceID() = %v, want objects 1=a 2=b", got)
	}
}

func TestReadDeviceIDRejectsTruncatedObjects(t *testing.T) {
	s := newFakeServer(t, func(_ uint8, _ []byte) []byte {
		// Announces one object of eight bytes but carries three.
		return []byte{funcReadDeviceID, meiDeviceID, ReadDevIDBasic, 0x01, 0x00, 0x00, 0x01, 0x00, 8, 'a', 'b', 'c'}
	})
	c := New(s.addr(), time.Second)
	t.Cleanup(func() { _ = c.Close() })

	if _, err := c.ReadDeviceID(context.Background(), 0, ReadDevIDBasic, ObjectVendor); err == nil {
		t.Error("expected error")
	}
}

func TestConnectRefused(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()

	c := New(addr, 200*time.Millisecond)
	if err := c.Connect(context.Background()); err == nil {
		t.Error("Connect() to a closed port: expected error")
	}
}
