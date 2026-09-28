package modbus

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"
)

func TestWriteSingleRegister(t *testing.T) {
	var got []byte
	s := newFakeServer(t, func(_ uint8, pdu []byte) []byte {
		got = append([]byte(nil), pdu...)

		return pdu
	})
	w := NewWriter(New(s.addr(), time.Second))

	if err := w.WriteSingleRegister(context.Background(), 0, 41948, 300); err != nil {
		t.Fatalf("WriteSingleRegister() error = %v", err)
	}

	want := []byte{0x06, 0xA3, 0xDC, 0x01, 0x2C}
	if !bytes.Equal(got, want) {
		t.Errorf("request PDU = % X, want % X", got, want)
	}
}

func TestWriteMultipleRegisters(t *testing.T) {
	var got []byte
	s := newFakeServer(t, func(_ uint8, pdu []byte) []byte {
		got = append([]byte(nil), pdu...)

		return pdu[:5]
	})
	w := NewWriter(New(s.addr(), time.Second))

	if err := w.WriteMultipleRegisters(context.Background(), 0, 40381, []uint16{0xFFFF, 0xFF9C}); err != nil {
		t.Fatalf("WriteMultipleRegisters() error = %v", err)
	}

	want := []byte{0x10, 0x9D, 0xBD, 0x00, 0x02, 0x04, 0xFF, 0xFF, 0xFF, 0x9C}
	if !bytes.Equal(got, want) {
		t.Errorf("request PDU = % X, want % X", got, want)
	}
}

func TestWriteRejectsBadResponse(t *testing.T) {
	tests := []struct {
		name  string
		reply func(pdu []byte) []byte
		write func(w *Writer) error
	}{
		{
			name:  "0x06 echo with another value",
			reply: func(pdu []byte) []byte { return []byte{0x06, pdu[1], pdu[2], 0x00, 0x00} },
			write: func(w *Writer) error { return w.WriteSingleRegister(context.Background(), 0, 41948, 300) },
		},
		{
			name:  "0x10 reply with another quantity",
			reply: func(pdu []byte) []byte { return []byte{0x10, pdu[1], pdu[2], 0x00, 0x01} },
			write: func(w *Writer) error {
				return w.WriteMultipleRegisters(context.Background(), 0, 41948, []uint16{300, 1})
			},
		},
		{
			name:  "0x10 short reply",
			reply: func([]byte) []byte { return []byte{0x10, 0xA3} },
			write: func(w *Writer) error {
				return w.WriteMultipleRegisters(context.Background(), 0, 41948, []uint16{300})
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newFakeServer(t, func(_ uint8, pdu []byte) []byte { return tt.reply(pdu) })
			w := NewWriter(New(s.addr(), time.Second))

			if err := tt.write(w); err == nil {
				t.Error("expected error")
			}
		})
	}
}

func TestWriteException(t *testing.T) {
	s := newFakeServer(t, func(_ uint8, pdu []byte) []byte {
		return []byte{pdu[0] | exceptionBit, byte(ExceptionNoPermission)}
	})
	w := NewWriter(New(s.addr(), time.Second))

	err := w.WriteSingleRegister(context.Background(), 0, 41948, 300)

	var exc Exception
	if !errors.As(err, &exc) || exc != ExceptionNoPermission {
		t.Errorf("error = %v, want exception 0x80", err)
	}
}

func TestWriteMultipleRegistersRejectsBadCount(t *testing.T) {
	w := NewWriter(New("127.0.0.1:1", time.Second))

	if err := w.WriteMultipleRegisters(context.Background(), 0, 41948, nil); err == nil {
		t.Error("count 0: expected error")
	}
	if err := w.WriteMultipleRegisters(context.Background(), 0, 41948, make([]uint16, MaxWriteCount+1)); err == nil {
		t.Error("count above the limit: expected error")
	}
}
