package server

import (
	"log/slog"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

type uiConnection struct {
	conn *websocket.Conn
	s    *Server

	send chan interface{}
	done chan struct{}

	mu sync.Mutex
}

func newUIConnection(conn *websocket.Conn, s *Server) *uiConnection {
	return &uiConnection{
		conn: conn,
		s:    s,
		send: make(chan interface{}, 64),
		done: make(chan struct{}),
	}
}

func (c *uiConnection) run() {
	defer func() {
		close(c.done)
		_ = c.conn.Close()
		c.s.unregisterUI(c)
	}()

	// Require a pong (browsers auto-reply to pings) within pongWait, refreshed on
	// every pong, so a closed laptop / dropped network is detected promptly instead
	// of leaking this goroutine pair plus the registry entry.
	_ = c.conn.SetReadDeadline(time.Now().Add(pongWait))
	c.conn.SetPongHandler(func(string) error {
		return c.conn.SetReadDeadline(time.Now().Add(pongWait))
	})

	go c.writeLoop()
	c.readLoop()
}

func (c *uiConnection) readLoop() {
	// Closing the conn unblocks writeLoop's pending write/ping when read fails.
	defer func() { _ = c.conn.Close() }()
	for {
		_, _, err := c.conn.ReadMessage()
		if err != nil {
			c.log().With(slog.Any("error", err)).Debug("ui connection closed")
			return
		}
		_ = c.conn.SetReadDeadline(time.Now().Add(pongWait))
	}
}

func (c *uiConnection) writeLoop() {
	ticker := time.NewTicker(pingPeriod)
	defer ticker.Stop()
	defer func() { _ = c.conn.Close() }()

	for {
		select {
		case <-c.done:
			return
		case msg := <-c.send:
			_ = c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := c.writeMessage(msg); err != nil {
				c.log().With(slog.Any("error", err)).Warn("ui write error")
				return
			}
		case <-ticker.C:
			if err := c.conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(writeWait)); err != nil {
				c.log().With(slog.Any("error", err)).Warn("ui ping failed")
				return
			}
		}
	}
}

// sendJSON enqueues a per-client value that the write loop will JSON-encode.
// Used for messages unique to one client (e.g. the initial snapshot).
func (c *uiConnection) sendJSON(v interface{}) {
	select {
	case c.send <- v:
	default:
		c.log().Warn("closing ui connection; buffer full")
		c.close()
	}
}

// sendBytes enqueues a pre-marshaled broadcast payload shared by all UI clients
// (serialized once in Server.broadcastUI) so the write loop can send it verbatim.
func (c *uiConnection) sendBytes(b []byte) {
	select {
	case c.send <- b:
	default:
		c.log().Warn("closing ui connection; buffer full")
		c.close()
	}
}

// writeMessage sends either a pre-marshaled broadcast payload ([]byte) or a
// per-client value still needing JSON encoding.
func (c *uiConnection) writeMessage(msg interface{}) error {
	if b, ok := msg.([]byte); ok {
		return c.conn.WriteMessage(websocket.TextMessage, b)
	}
	return c.conn.WriteJSON(msg)
}

func (c *uiConnection) close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	select {
	case <-c.done:
	default:
		_ = c.conn.Close()
	}
}

func (c *uiConnection) log() *slog.Logger {
	return c.s.log.With(slog.String("component", "ui"))
}
