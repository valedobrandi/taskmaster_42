package internal

import (
	"fmt"
	"io"
	"net"
	"sync"
)

type attachSink struct {
	mu   sync.Mutex
	conn net.Conn
}

type streamRouter struct {
	stdout *attachSink
	stderr *attachSink
}

func (s *attachSink) Attach(conn net.Conn) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.conn != nil {
		return fmt.Errorf("already attached")
	}
	s.conn = conn
	return nil
}

func (s *attachSink) Detach(conn net.Conn) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.conn == conn {
		s.conn = nil
	}
}

func (s *attachSink) Write(p []byte) (n int, err error) {
	s.mu.Lock()
	conn := s.conn
	s.mu.Unlock()

	if conn == nil {
		// Discard
		return len(p), nil
	}

	if _, err := conn.Write(p); err != nil {
		s.mu.Lock()
		if s.conn == conn {
			s.conn = nil
		}
		s.mu.Unlock()
	}
	return len(p), nil
}

func newStreamRoute() *streamRouter {
	return &streamRouter{
		stdout: &attachSink{},
		stderr: &attachSink{},
	}
}

func (r *streamRouter) stdoutWrite(base io.Writer) io.Writer {
	return io.MultiWriter(base, r.stdout)
}

func (r *streamRouter) stderrWrite(base io.Writer) io.Writer {
	return io.MultiWriter(base, r.stderr)
}
