// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build linux

package core

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"remote21/internal/serial"
)

// openPTY returns the master and the slave device path.
func openPTY(t *testing.T) (*os.File, string) {
	m, err := os.OpenFile("/dev/ptmx", os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		t.Skip("no pty:", err)
	}
	var n uint32
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, m.Fd(), syscall.TIOCGPTN, uintptr(unsafe.Pointer(&n))); e != 0 {
		t.Skip("TIOCGPTN", e)
	}
	var unlock int32
	syscall.Syscall(syscall.SYS_IOCTL, m.Fd(), syscall.TIOCSPTLCK, uintptr(unsafe.Pointer(&unlock)))
	return m, fmt.Sprintf("/dev/pts/%d", n)
}

// The full stack over a real tty: termios raw mode, reader goroutine,
// framing, polling and command sending.
func TestOverPTY(t *testing.T) {
	master, dev := openPTY(t)
	defer master.Close()
	sim := NewDemoSim()
	// bridge: bytes from the tty go into the simulator, answers go back
	go func() {
		b := make([]byte, 256)
		for {
			n, err := master.Read(b)
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
				master.Write(out[:k])
			}
		}
	}()
	cfg := LoadConfig(filepath.Join(t.TempDir(), "s.xml"))
	a := NewApp(cfg)
	a.Opener = func(n string) (serial.Port, error) { return serial.Open(n, 115200) }
	a.mu.Lock()
	a.openPortLocked(dev)
	a.mu.Unlock()
	if a.link == nil {
		t.Fatal(a.portErr)
	}
	go a.senderLoop()
	a.Poll()
	st := a.State()
	if !st.Online || len(st.Strips) < 20 {
		t.Fatalf("poll over pty failed: %+v online=%v strips=%d", st.Rack, st.Online, len(st.Strips))
	}
	a.Do(Op{Op: "gpo", Slot: 8, Index: 9, Value: true})
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		var got []byte
		sim.Mutate(func(s *SimPort) { got = s.Received[8] })
		if len(got) == 2 && got[1]&0x02 != 0 {
			a.Close()
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("GPO command did not arrive over the tty")
}
