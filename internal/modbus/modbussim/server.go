// Package modbussim is an in-process Modbus-TCP server for tests and for
// developing against a device that is not to hand.
//
// It answers the same subset the client speaks — 0x03 and 0x2B/0x0E — out of a
// register map that a test can seed and mutate while a client is polling, which
// is what makes it possible to rehearse behaviour that depends on values
// changing over time without waiting on real equipment.
package modbussim

import (
	"encoding/binary"
	"io"
	"net"
	"sync"
)

const (
	mbapLen          = 7
	funcReadHolding  = 0x03
	funcReadDeviceID = 0x2B
	meiDeviceID      = 0x0E
	exceptionBit     = 0x80

	exceptionInvalidFunction = 0x01
	exceptionInvalidAddress  = 0x02
)

// Server serves a register map over Modbus-TCP on a loopback port.
type Server struct {
	ln net.Listener

	mu        sync.Mutex
	regs      map[uint16]uint16
	objects   map[uint8]string
	unit      uint8
	requests  int
	failEvery int
	seen      int
}

// New starts a server on an arbitrary loopback port. Close it when finished.
// Registers absent from regs are answered with exception 0x02, which is what a
// real device does for an address its firmware does not implement.
func New(regs map[uint16]uint16) (*Server, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}

	s := &Server{
		ln:      ln,
		regs:    map[uint16]uint16{},
		objects: map[uint8]string{},
	}
	for a, v := range regs {
		s.regs[a] = v
	}

	go s.serve()

	return s, nil
}

// Addr is the host:port the server listens on.
func (s *Server) Addr() string { return s.ln.Addr().String() }

// Close stops the server.
func (s *Server) Close() error { return s.ln.Close() }

// Set writes a register, so a test can move a value while a client polls.
func (s *Server) Set(addr, value uint16) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.regs[addr] = value
}

// SetWords writes consecutive registers starting at addr.
func (s *Server) SetWords(addr uint16, words []uint16) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for i, w := range words {
		s.regs[addr+uint16(i)] = w
	}
}

// SetObject publishes a device identification object.
func (s *Server) SetObject(id uint8, value string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.objects[id] = value
}

// SetUnit restricts the server to one unit id; requests for any other are
// ignored, as a gateway does for a device that is not behind it.
func (s *Server) SetUnit(unit uint8) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.unit = unit
}

// FailEvery makes every nth request answer "slave busy", to exercise retries.
// Zero disables it.
func (s *Server) FailEvery(n int) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.failEvery = n
}

// Requests is the number of requests served.
func (s *Server) Requests() int {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.requests
}

func (s *Server) serve() {
	for {
		conn, err := s.ln.Accept()
		if err != nil {
			return
		}
		go s.handleConn(conn)
	}
}

func (s *Server) handleConn(conn net.Conn) {
	defer func() { _ = conn.Close() }()

	for {
		var head [mbapLen]byte
		if _, err := io.ReadFull(conn, head[:]); err != nil {
			return
		}

		length := int(binary.BigEndian.Uint16(head[4:]))
		if length < 2 {
			return
		}
		pdu := make([]byte, length-1)
		if _, err := io.ReadFull(conn, pdu); err != nil {
			return
		}

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

func (s *Server) handle(unit uint8, pdu []byte) []byte {
	s.mu.Lock()
	defer s.mu.Unlock()

	if unit != s.unit {
		return nil // silence, as a gateway gives for an unknown device
	}

	s.requests++
	s.seen++
	if s.failEvery > 0 && s.seen%s.failEvery == 0 {
		return []byte{pdu[0] | exceptionBit, 0x06} // slave busy
	}

	switch pdu[0] {
	case funcReadHolding:
		return s.readHolding(pdu)
	case funcReadDeviceID:
		return s.readDeviceID(pdu)
	default:
		return []byte{pdu[0] | exceptionBit, exceptionInvalidFunction}
	}
}

func (s *Server) readHolding(pdu []byte) []byte {
	if len(pdu) < 5 {
		return []byte{funcReadHolding | exceptionBit, exceptionInvalidAddress}
	}

	addr := binary.BigEndian.Uint16(pdu[1:])
	count := binary.BigEndian.Uint16(pdu[3:])

	out := make([]byte, 0, 2+2*count)
	out = append(out, funcReadHolding, byte(count*2))

	for i := range count {
		v, ok := s.regs[addr+i]
		if !ok {
			return []byte{funcReadHolding | exceptionBit, exceptionInvalidAddress}
		}
		out = append(out, byte(v>>8), byte(v))
	}

	return out
}

// readDeviceID answers with every published object at once, setting More to 0.
// The client's continuation handling is exercised separately in its own tests.
func (s *Server) readDeviceID(pdu []byte) []byte {
	if len(pdu) < 4 || pdu[1] != meiDeviceID {
		return []byte{funcReadDeviceID | exceptionBit, exceptionInvalidFunction}
	}

	readCode, first := pdu[2], pdu[3]

	ids := make([]int, 0, len(s.objects))
	for id := range s.objects {
		if id >= first {
			ids = append(ids, int(id))
		}
	}
	for i := 1; i < len(ids); i++ {
		for j := i; j > 0 && ids[j] < ids[j-1]; j-- {
			ids[j], ids[j-1] = ids[j-1], ids[j]
		}
	}

	if len(ids) == 0 {
		return []byte{funcReadDeviceID | exceptionBit, exceptionInvalidAddress}
	}

	out := []byte{funcReadDeviceID, meiDeviceID, readCode, readCode, 0x00, 0x00, byte(len(ids))}
	for _, id := range ids {
		v := s.objects[uint8(id)]
		out = append(out, byte(id), byte(len(v)))
		out = append(out, v...)
	}

	return out
}
