// SPDX-License-Identifier: AGPL-3.0-or-later
// Package netport carries the D21m serial byte stream over TCP, for serial
// device servers (RS-232 to Ethernet converters).
//
//	tcp://host:port         we connect; the device server port is in "TCP Server" mode
//	tcp-listen://:port      the device server connects to us; its port is in "TCP Client" mode
//
// The serial parameters (115200 8N1, no flow control) are set on the device server itself.
package netport

import (
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"
)

const (
	PrefixClient = "tcp://"
	PrefixListen = "tcp-listen://"
)

// IsNet reports whether name is a network port address.
func IsNet(name string) bool {
	return strings.HasPrefix(name, PrefixClient) || strings.HasPrefix(name, PrefixListen)
}

// Port implements the core's serial.Port interface over TCP.
type Port struct {
	name   string
	addr   string
	listen bool

	mu       sync.Mutex
	conn     net.Conn
	ln       net.Listener
	closed   bool
	lastErr  string
	nextDial time.Time

	rx  chan []byte
	buf []byte
}

// ErrNotConnected is returned while no device server is connected.
var ErrNotConnected = errors.New("not connected")

// Open parses name and prepares the port. A client port connects lazily (and
// reconnects after a drop) so the app can start before the device server is reachable.
// A listening port starts accepting immediately; it fails only if the TCP port
// cannot be opened.
func Open(name string) (*Port, error) {
	p := &Port{name: name, rx: make(chan []byte, 256)}
	switch {
	case strings.HasPrefix(name, PrefixClient):
		p.addr = strings.TrimSuffix(strings.TrimPrefix(name, PrefixClient), "/")
		if _, _, err := net.SplitHostPort(p.addr); err != nil {
			return nil, fmt.Errorf("address must be host:port, e.g. 192.168.127.254:4001")
		}
		p.mu.Lock()
		p.dialLocked()
		p.mu.Unlock()
	case strings.HasPrefix(name, PrefixListen):
		p.listen = true
		p.addr = strings.TrimSuffix(strings.TrimPrefix(name, PrefixListen), "/")
		if !strings.Contains(p.addr, ":") {
			p.addr = ":" + p.addr
		}
		ln, err := net.Listen("tcp", p.addr)
		if err != nil {
			if strings.Contains(err.Error(), "in use") {
				return nil, fmt.Errorf("TCP port %s is already used by another program", portOf(p.addr))
			}
			return nil, fmt.Errorf("cannot listen on TCP port %s: %v", portOf(p.addr), err)
		}
		p.ln = ln
		go p.acceptLoop()
	default:
		return nil, fmt.Errorf("not a network port: %s", name)
	}
	return p, nil
}

func (p *Port) Name() string { return p.name }

// Status describes the connection for the UI.
func (p *Port) Status() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.conn != nil {
		return "Connected to " + p.conn.RemoteAddr().String()
	}
	if p.listen {
		return "Waiting for the device server to connect on TCP port " + portOf(p.addr)
	}
	if p.lastErr != "" {
		return "Not connected to " + p.addr + ": " + p.lastErr
	}
	return "Not connected to " + p.addr
}

func portOf(addr string) string {
	if _, port, err := net.SplitHostPort(addr); err == nil {
		return port
	}
	return addr
}

func tune(c net.Conn) {
	if tc, ok := c.(*net.TCPConn); ok {
		tc.SetNoDelay(true)
		tc.SetKeepAlive(true)
		tc.SetKeepAlivePeriod(15 * time.Second)
	}
}

// dialLocked tries to connect (client mode), at most once every 2 s.
func (p *Port) dialLocked() {
	if p.conn != nil || p.closed || time.Now().Before(p.nextDial) {
		return
	}
	p.nextDial = time.Now().Add(2 * time.Second)
	c, err := net.DialTimeout("tcp", p.addr, 1500*time.Millisecond)
	if err != nil {
		p.lastErr = shortErr(err)
		return
	}
	p.lastErr = ""
	tune(c)
	p.attachLocked(c)
}

func shortErr(err error) string {
	s := err.Error()
	if i := strings.LastIndex(s, ": "); i >= 0 {
		s = s[i+2:]
	}
	return s
}

func (p *Port) acceptLoop() {
	for {
		c, err := p.ln.Accept()
		if err != nil {
			return
		}
		tune(c)
		p.mu.Lock()
		if p.closed {
			p.mu.Unlock()
			c.Close()
			return
		}
		// the newest connection wins (the device server reconnects after a reboot)
		if p.conn != nil {
			p.conn.Close()
		}
		p.attachLocked(c)
		p.mu.Unlock()
	}
}

func (p *Port) attachLocked(c net.Conn) {
	p.conn = c
	go p.readLoop(c)
}

func (p *Port) readLoop(c net.Conn) {
	b := make([]byte, 1024)
	for {
		n, err := c.Read(b)
		if n > 0 {
			d := make([]byte, n)
			copy(d, b[:n])
			select {
			case p.rx <- d:
			default:
			}
		}
		if err != nil {
			p.mu.Lock()
			if p.conn == c {
				p.conn = nil
				p.lastErr = "connection closed"
			}
			p.mu.Unlock()
			c.Close()
			return
		}
	}
}

func (p *Port) Write(b []byte) (int, error) {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return 0, errors.New("port closed")
	}
	if !p.listen {
		p.dialLocked()
	}
	c := p.conn
	p.mu.Unlock()
	if c == nil {
		return 0, ErrNotConnected
	}
	c.SetWriteDeadline(time.Now().Add(2 * time.Second))
	n, err := c.Write(b)
	if err != nil {
		p.mu.Lock()
		if p.conn == c {
			p.conn = nil
			p.lastErr = shortErr(err)
		}
		p.mu.Unlock()
		c.Close()
	}
	return n, err
}

func (p *Port) ReadTimeout(dst []byte, d time.Duration) (int, error) {
	if len(p.buf) == 0 {
		select {
		case c := <-p.rx:
			p.buf = c
		case <-time.After(d):
			return 0, nil
		}
	}
	n := copy(dst, p.buf)
	p.buf = p.buf[n:]
	return n, nil
}

func (p *Port) Flush() {
	p.buf = nil
	for {
		select {
		case <-p.rx:
		default:
			return
		}
	}
}

func (p *Port) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.closed = true
	if p.ln != nil {
		p.ln.Close()
	}
	if p.conn != nil {
		p.conn.Close()
		p.conn = nil
	}
	return nil
}
