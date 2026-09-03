package tunnel

import (
	"io"
	"net"
	"time"
)

type closeWriter interface {
	CloseWrite() error
}

// relayDrainGrace is how long the surviving direction of a relay may keep
// delivering buffered data after the other direction fails (e.g. the target
// RSTs mid-response). Without this grace, an abrupt error on one side
// hard-closes both conns and silently drops the tail of the other side's data.
const relayDrainGrace = 2 * time.Second

func relayBidirectional(a, b net.Conn, observeBytes func(int64)) error {
	errc := make(chan error, 2)
	go copyAndCloseWrite(a, b, observeBytes, errc)
	go copyAndCloseWrite(b, a, observeBytes, errc)

	var firstErr error
	for i := 0; i < 2; i++ {
		err := <-errc
		if err == nil || expectedRelayClose(err) {
			continue
		}
		if firstErr != nil {
			continue
		}
		firstErr = err
		// The failing direction's copy already returned. Let the surviving
		// direction drain in-flight data for a short window instead of
		// discarding it, then force-close both conns.
		if i == 0 {
			select {
			case <-errc:
				i++
			case <-time.After(relayDrainGrace):
				i++
			}
		}
		_ = a.Close()
		_ = b.Close()
	}
	return firstErr
}

func copyAndCloseWrite(dst, src net.Conn, observeBytes func(int64), errc chan<- error) {
	n, err := io.Copy(dst, src)
	if observeBytes != nil {
		observeBytes(n)
	}
	if closeErr := closeWrite(dst); err == nil {
		err = closeErr
	}
	errc <- err
}

func closeWrite(conn net.Conn) error {
	if conn == nil {
		return nil
	}
	if closer, ok := conn.(closeWriter); ok {
		return closer.CloseWrite()
	}
	return conn.Close()
}
