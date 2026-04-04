package tunnel

import (
	"io"
	"net"
	"time"

	"github.com/gorilla/websocket"
)

// wsConn wraps a *websocket.Conn up into a net.Conn.
type wsConn struct {
	*websocket.Conn
	reader io.Reader
}

func NewWSConn(c *websocket.Conn) net.Conn {
	return &wsConn{Conn: c}
}

func (c *wsConn) Read(b []byte) (int, error) {
	if c.reader == nil {
		_, reader, err := c.Conn.NextReader()
		if err != nil {
			return 0, err
		}
		c.reader = reader
	}

	n, err := c.reader.Read(b)
	if err == io.EOF {
		c.reader = nil
		// Return the bytes read so far, actual EOF will be returned on next read empty
		if n > 0 {
			return n, nil
		}
		// Try next message immediately
		return c.Read(b)
	}
	return n, err
}

func (c *wsConn) Write(b []byte) (int, error) {
	err := c.Conn.WriteMessage(websocket.BinaryMessage, b)
	if err != nil {
		return 0, err
	}
	return len(b), nil
}

func (c *wsConn) SetDeadline(t time.Time) error {
	if err := c.Conn.SetReadDeadline(t); err != nil {
		return err
	}
	return c.Conn.SetWriteDeadline(t)
}
