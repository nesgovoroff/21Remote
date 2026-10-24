// SPDX-License-Identifier: AGPL-3.0-or-later
package core

import (
	"fmt"
	"net"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeServer bridges one TCP connection to a simulated D21m, like a serial device server
// passing bytes between its Ethernet side and the serial line.
func fakeServer(c net.Conn, sim *SimPort) {
	defer c.Close()
	b := make([]byte, 256)
	for {
		n, err := c.Read(b)
		if err != nil {
			return
		}
		sim.Write(b[:n])
		out := make([]byte, 512)
		for {
			k, _ := sim.ReadTimeout(out, 2*time.Millisecond)
			if k == 0 {
				break
			}
			c.Write(out[:k])
		}
	}
}

func appWithPort(t *testing.T, name string) *App {
	a := NewApp(LoadConfig(filepath.Join(t.TempDir(), "s.xml")))
	a.mu.Lock()
	a.openPortLocked(name)
	a.mu.Unlock()
	if a.link == nil {
		t.Fatalf("open %s: %s", name, a.portErr)
	}
	go a.senderLoop()
	t.Cleanup(a.Close)
	return a
}

func strips(a *App) int { return len(a.State().Strips) }

// Device server in TCP Server mode: the app connects to it, and reconnects after a drop.
func TestDeviceServerTCPServerMode(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	sim := NewDemoSim()
	conns := make(chan net.Conn, 4)
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			conns <- c
			go fakeServer(c, sim)
		}
	}()
	a := appWithPort(t, "tcp://"+ln.Addr().String())
	a.Poll()
	if n := strips(a); n < 20 || !strings.HasPrefix(a.State().PortStatus, "Connected") {
		t.Fatalf("poll over TCP failed: %d strips, status %q", n, a.State().PortStatus)
	}
	// device server reboots: connection drops
	(<-conns).Close()
	time.Sleep(50 * time.Millisecond)
	if s := a.State().PortStatus; strings.HasPrefix(s, "Connected") {
		t.Fatalf("drop not noticed: %q", s)
	}
	time.Sleep(2100 * time.Millisecond) // redial back-off
	a.Poll()
	if !strings.HasPrefix(a.State().PortStatus, "Connected") || a.State().Rack.Module != "D21m LVDS HD" {
		t.Fatalf("no reconnect: %q", a.State().PortStatus)
	}
	a.Do(Op{Op: "dl5", Strip: "s2c3", Field: "phantom", Value: true})
	waitFor(t, func() bool {
		var got []byte
		sim.Mutate(func(s *SimPort) { got = s.Received[2] })
		return len(got) == 16 && got[4]&0x80 != 0
	})
}

// Device server in TCP Client mode: it connects to the app.
func TestDeviceServerTCPClientMode(t *testing.T) {
	probe, _ := net.Listen("tcp", "127.0.0.1:0")
	port := probe.Addr().(*net.TCPAddr).Port
	probe.Close()
	a := appWithPort(t, fmt.Sprintf("tcp-listen://127.0.0.1:%d", port))
	if s := a.State().PortStatus; !strings.Contains(s, "Waiting") {
		t.Fatalf("status %q", s)
	}
	a.Poll()
	if a.State().Rack.Module != "Not Connected" {
		t.Fatal("must be offline before the device server connects")
	}
	sim := NewDemoSim()
	c, err := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		t.Fatal(err)
	}
	go fakeServer(c, sim)
	waitFor(t, func() bool { return strings.HasPrefix(a.State().PortStatus, "Connected") })
	a.Poll()
	if n := strips(a); n < 20 {
		t.Fatalf("poll via incoming connection failed: %d strips", n)
	}
	// a busy TCP port is reported, not silently ignored
	b := NewApp(LoadConfig(filepath.Join(t.TempDir(), "s.xml")))
	b.mu.Lock()
	b.openPortLocked(fmt.Sprintf("tcp-listen://127.0.0.1:%d", port))
	b.mu.Unlock()
	if b.link != nil || b.portErr == "" {
		t.Fatal("second listener on the same port must fail with a message")
	}
}

func waitFor(t *testing.T, f func() bool) {
	t.Helper()
	for end := time.Now().Add(2 * time.Second); time.Now().Before(end); time.Sleep(10 * time.Millisecond) {
		if f() {
			return
		}
	}
	t.Fatal("condition not reached")
}
