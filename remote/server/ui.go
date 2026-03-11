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

	go c.writeLoop()
	c.readLoop()
}

func (c *uiConnection) readLoop() {
	for {
		_, _, err := c.conn.ReadMessage()
		if err != nil {
			c.log().With(slog.Any("error", err)).Debug("ui connection closed")
			return
		}
	}
}

func (c *uiConnection) writeLoop() {
	for {
		select {
		case <-c.done:
			return
		case msg := <-c.send:
			if err := c.conn.WriteJSON(msg); err != nil {
				c.log().With(slog.Any("error", err)).Warn("ui write error")
				return
			}
		case <-time.After(30 * time.Second):
			if err := c.conn.WriteControl(websocket.PingMessage, []byte("ping"), time.Now().Add(5*time.Second)); err != nil {
				c.log().With(slog.Any("error", err)).Warn("ui ping failed")
				return
			}
		}
	}
}

func (c *uiConnection) sendJSON(v interface{}) {
	select {
	case c.send <- v:
	default:
		c.log().Warn("closing ui connection; buffer full")
		c.close()
	}
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
