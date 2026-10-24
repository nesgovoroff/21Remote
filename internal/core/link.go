// SPDX-License-Identifier: AGPL-3.0-or-later
package core

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"remote21/internal/proto"
	"remote21/internal/serial"
)

// ErrTimeout is returned when the frame does not answer in time.
var ErrTimeout = errors.New("timeout")

// LogEntry is one line of the communication log shown in Settings.
type LogEntry struct {
	T    string `json:"t"`
	Dir  string `json:"dir"` // "tx", "rx", "err", "info"
	Text string `json:"text"`
}

// Link serialises request/response transactions on a serial port
// (synchronous request/answer over the port).
type Link struct {
	mu      sync.Mutex
	port    serial.Port
	timeout time.Duration

	logMu sync.Mutex
	log   []LogEntry
	trace bool
}

func NewLink(p serial.Port) *Link {
	return &Link{port: p, timeout: 200 * time.Millisecond}
}

func (l *Link) Close() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.port != nil {
		l.port.Close()
		l.port = nil
	}
}

func (l *Link) Name() string {
	if l == nil || l.port == nil {
		return ""
	}
	return l.port.Name()
}

func (l *Link) SetTrace(on bool) {
	l.logMu.Lock()
	l.trace = on
	l.logMu.Unlock()
}

func (l *Link) addLog(dir, text string) {
	l.logMu.Lock()
	defer l.logMu.Unlock()
	if !l.trace && (dir == "tx" || dir == "rx") {
		return
	}
	l.log = append(l.log, LogEntry{T: time.Now().Format("15:04:05.000"), Dir: dir, Text: text})
	if len(l.log) > 400 {
		l.log = l.log[len(l.log)-400:]
	}
}

func (l *Link) Log() []LogEntry {
	l.logMu.Lock()
	defer l.logMu.Unlock()
	out := make([]LogEntry, len(l.log))
	copy(out, l.log)
	return out
}

func hexs(b []byte) string {
	var s strings.Builder
	for i, x := range b {
		if i > 0 {
			s.WriteByte(' ')
		}
		fmt.Fprintf(&s, "%02X", x)
	}
	return s.String()
}

// Transact sends req and waits for one response frame.
func (l *Link) Transact(req []byte) ([]byte, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.port == nil {
		return nil, serial.ErrClosed
	}
	l.port.Flush()
	l.addLog("tx", hexs(req))
	if _, err := l.port.Write(req); err != nil {
		l.addLog("err", "write: "+err.Error())
		return nil, err
	}
	deadline := time.Now().Add(l.timeout)
	var frame []byte
	buf := make([]byte, 256)
	for {
		left := time.Until(deadline)
		if left <= 0 {
			l.addLog("err", "timeout waiting for answer to "+hexs(req))
			return nil, ErrTimeout
		}
		n, err := l.port.ReadTimeout(buf, left)
		if err != nil {
			l.addLog("err", "read: "+err.Error())
			return nil, err
		}
		for _, b := range buf[:n] {
			if proto.IsStart(b) && len(frame) > 0 {
				frame = frame[:0] // resynchronise on a new start byte
			}
			frame = append(frame, b)
			if proto.IsTerminator(b) && len(frame) >= 2 {
				l.addLog("rx", hexs(frame))
				if len(frame) >= 3 {
					if ck := proto.Checksum(frame[1 : len(frame)-1]); ck != frame[len(frame)-1] {
						l.addLog("info", fmt.Sprintf("checksum differs (got %02X, calc %02X)", frame[len(frame)-1], ck))
					}
				}
				return frame, nil
			}
		}
	}
}

// Send writes a frame without waiting for an answer.
// It retries up to 10 times on port errors.
func (l *Link) Send(f []byte) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.port == nil {
		return serial.ErrClosed
	}
	var err error
	for i := 0; i < 10; i++ {
		l.addLog("tx", hexs(f))
		if _, err = l.port.Write(f); err == nil {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	l.addLog("err", "Serial port error while sending D21m command. Tried 10 times: "+err.Error())
	return err
}

func (l *Link) word(req []byte) (int, error) {
	resp, err := l.Transact(req)
	if err != nil {
		return 0, err
	}
	if err := proto.CheckError(resp); err != nil {
		return 0, err
	}
	return proto.Word16(resp)
}

func (l *Link) ModuleType(addr byte) (int, error) { return l.word(proto.ModuleTypeRequest(addr)) }
func (l *Link) Wellness(addr byte) (int, error)   { return l.word(proto.WellnessRequest(addr)) }

func (l *Link) SlotStatus(slot int) (proto.SlotStatus, error) {
	resp, err := l.Transact(proto.SlotStatusRequest(slot))
	if err != nil {
		return proto.SlotStatus{}, err
	}
	if err := proto.CheckError(resp); err != nil {
		return proto.SlotStatus{}, err
	}
	if resp[1] != proto.RespD21mStat {
		return proto.SlotStatus{}, fmt.Errorf("unexpected answer 0x%02X to slot status", resp[1])
	}
	return proto.ParseSlotStatus(resp)
}

func (l *Link) Command(slot int) ([]byte, error) {
	resp, err := l.Transact(proto.CommandRequest(slot))
	if err != nil {
		return nil, err
	}
	if err := proto.CheckError(resp); err != nil {
		return nil, err
	}
	return proto.CommandFromResponse(resp), nil
}

func (l *Link) SetCommand(slot int, cmd []byte) error { return l.Send(proto.SetCommand(slot, cmd)) }
