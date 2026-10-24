// SPDX-License-Identifier: AGPL-3.0-or-later
// Package serial opens POSIX serial ports (macOS /dev/cu.*, Linux /dev/tty*)
// in raw 8N1 mode using termios, without cgo.
package serial

import (
	"errors"
	"io"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Port is a byte stream to the device.
type Port interface {
	io.Writer
	// ReadTimeout reads available bytes, waiting at most d.
	ReadTimeout(p []byte, d time.Duration) (int, error)
	// Flush discards unread input.
	Flush()
	Close() error
	Name() string
}

// ErrClosed is returned after Close.
var ErrClosed = errors.New("serial port closed")

type ttyPort struct {
	name   string
	fd     int
	mu     sync.Mutex
	closed bool
	rx     chan []byte
	buf    []byte
	done   chan struct{}
}

// Open opens dev at the given baud rate, 8 data bits, no parity, 1 stop bit,
// no flow control, and drops DTR and RTS.
func Open(dev string, baud int) (Port, error) {
	fd, err := syscall.Open(dev, syscall.O_RDWR|syscall.O_NOCTTY|syscall.O_NONBLOCK|syscall.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	if err := setupTTY(fd, baud); err != nil {
		syscall.Close(fd)
		return nil, err
	}
	// Back to blocking mode; reads are bounded by VMIN=0/VTIME.
	if err := syscall.SetNonblock(fd, false); err != nil {
		syscall.Close(fd)
		return nil, err
	}
	p := &ttyPort{name: dev, fd: fd, rx: make(chan []byte, 256), done: make(chan struct{})}
	p.Flush()
	go p.reader()
	return p, nil
}

func (p *ttyPort) Name() string { return p.name }

func (p *ttyPort) reader() {
	defer close(p.done)
	b := make([]byte, 512)
	for {
		p.mu.Lock()
		closed := p.closed
		p.mu.Unlock()
		if closed {
			return
		}
		n, err := syscall.Read(p.fd, b)
		if n > 0 {
			c := make([]byte, n)
			copy(c, b[:n])
			select {
			case p.rx <- c:
			default: // overflow: drop oldest behaviour is fine for a request/response link
			}
		}
		if err != nil && err != syscall.EINTR && err != syscall.EAGAIN {
			// device vanished (USB adapter unplugged)
			p.mu.Lock()
			p.closed = true
			p.mu.Unlock()
			close(p.rx)
			return
		}
	}
}

func (p *ttyPort) Write(b []byte) (int, error) {
	p.mu.Lock()
	closed := p.closed
	p.mu.Unlock()
	if closed {
		return 0, ErrClosed
	}
	total := 0
	for total < len(b) {
		n, err := syscall.Write(p.fd, b[total:])
		if err == syscall.EINTR || err == syscall.EAGAIN {
			time.Sleep(time.Millisecond)
			continue
		}
		if err != nil {
			return total, err
		}
		total += n
	}
	return total, nil
}

func (p *ttyPort) ReadTimeout(dst []byte, d time.Duration) (int, error) {
	if len(p.buf) == 0 {
		select {
		case c, ok := <-p.rx:
			if !ok {
				return 0, ErrClosed
			}
			p.buf = c
		case <-time.After(d):
			return 0, nil
		}
	}
	n := copy(dst, p.buf)
	p.buf = p.buf[n:]
	return n, nil
}

func (p *ttyPort) Flush() {
	flushTTY(p.fd)
	p.buf = nil
	for {
		select {
		case _, ok := <-p.rx:
			if !ok {
				return
			}
		default:
			return
		}
	}
}

func (p *ttyPort) Close() error {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil
	}
	p.closed = true
	p.mu.Unlock()
	select {
	case <-p.done:
	case <-time.After(500 * time.Millisecond):
	}
	return syscall.Close(p.fd)
}

// List returns candidate serial devices, most likely ones first.
func List() []string {
	ports := listPorts()
	rank := func(s string) int {
		l := strings.ToLower(s)
		switch {
		case strings.Contains(l, "usbserial"), strings.Contains(l, "usbmodem"), strings.Contains(l, "ttyusb"), strings.Contains(l, "ttyacm"):
			return 0
		case strings.Contains(l, "serial"), strings.Contains(l, "wch"), strings.Contains(l, "slab"), strings.Contains(l, "pl2303"):
			return 1
		case strings.Contains(l, "bluetooth"), strings.Contains(l, "debug-console"):
			return 3
		}
		return 2
	}
	sort.SliceStable(ports, func(i, j int) bool {
		ri, rj := rank(ports[i]), rank(ports[j])
		if ri != rj {
			return ri < rj
		}
		return ports[i] < ports[j]
	})
	return ports
}
