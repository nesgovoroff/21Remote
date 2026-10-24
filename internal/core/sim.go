// SPDX-License-Identifier: AGPL-3.0-or-later
package core

import (
	"sync"
	"time"

	"remote21/internal/proto"
)

// SimPort is an in-process D21m frame simulator implementing serial.Port.
// It is used for the "Demo" port and for tests.
type SimPort struct {
	mu     sync.Mutex
	out    []byte
	cond   chan struct{}
	in     []byte
	closed bool

	Module   int
	Wellness int
	Slots    [12]SimSlot
	// Received records every SET D21M_CMD frame: slot -> last command
	Received map[int][]byte
	Frames   [][]byte
	// ReportSlot makes a slot answer with another slot number (tests)
	ReportSlot map[int]int
}

type SimSlot struct {
	Type      int
	I8S       bool
	VoltageOK bool
	AudioIn   int
	AudioOut  int
	Command   []byte
	Status    []byte
}

func NewDemoSim() *SimPort {
	s := &SimPort{cond: make(chan struct{}, 1), Module: proto.ModuleLVDS, Received: map[int][]byte{}}
	set := func(i, t, ain, aout, cmd, stat int) {
		s.Slots[i] = SimSlot{Type: t, VoltageOK: true, AudioIn: ain, AudioOut: aout, Command: make([]byte, cmd), Status: make([]byte, stat)}
	}
	set(0, MicEven, 4, 0, 8, 0)
	set(1, MicOdd, 4, 0, 8, 0)
	set(2, DL5Mic, 4, 0, 16, 0)
	set(3, AESEBUCardIOSFC, 4, 4, 4, 2)
	set(5, LineInCard, 4, 0, 0, 0)
	set(6, LineOutCard, 0, 4, 0, 0)
	set(7, ADAT16, 8, 8, 0, 1)
	s.Slots[7].I8S = false
	set(8, GPIOCard, 0, 0, 2, 2)
	set(10, MADIOptElect, 8, 8, 2, 2)
	s.Slots[10].I8S = true
	// some plausible initial state
	c := s.Slots[0].Command
	for ch := 1; ch <= 4; ch++ {
		tmp := &Card{Command: c, CommandSize: 8}
		micEncode(tmp, ch, MicState{Gain: 20 + ch*5, Phantom: ch%2 == 1, HPF: ch == 2})
	}
	d := s.Slots[2].Command
	for ch := 1; ch <= 8; ch++ {
		tmp := &Card{Command: d, CommandSize: 16}
		dl5Encode(tmp, ch, DL5State{Gain: ch * 6, DCS: true})
	}
	s.Slots[3].Status = []byte{0xF0, 0x01} // inputs 1-4 locked, ext not locked
	s.Slots[3].Command = []byte{0x00, 0x00, 0x0A, 0xF0}
	s.Slots[7].Status = []byte{0x02}       // bank 1 locked
	s.Slots[8].Status = []byte{0x05, 0x80} // GPI 1,3,16 active
	s.Slots[10].Command = []byte{0x12, 0x34}
	s.Slots[10].Status = []byte{0xAB, 0xCD}
	return s
}

func (s *SimPort) Name() string { return "Demo" }

func (s *SimPort) Write(b []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, x := range b {
		if proto.IsStart(x) {
			s.in = s.in[:0]
		}
		s.in = append(s.in, x)
		if proto.IsTerminator(x) && len(s.in) >= 2 {
			s.handle(append([]byte(nil), s.in...))
			s.in = s.in[:0]
		}
	}
	return len(b), nil
}

func (s *SimPort) reply(payload ...byte) {
	f := append([]byte{0x80}, payload...)
	f = append(f, proto.Checksum(payload))
	s.out = append(s.out, f...)
	select {
	case s.cond <- struct{}{}:
	default:
	}
}

func (s *SimPort) handle(f []byte) {
	s.Frames = append(s.Frames, f)
	if len(f) < 3 {
		return
	}
	p := f[1 : len(f)-1]
	if proto.Checksum(p) != f[len(f)-1] {
		s.reply(proto.RespError, proto.ErrChecksum)
		return
	}
	switch {
	case len(p) == 2 && p[0] == proto.CmdReq && p[1] == proto.ReqModType:
		s.reply(proto.RespModType, byte(s.Module>>8), byte(s.Module&0xFF))
	case len(p) == 2 && p[0] == proto.CmdReq && p[1] == proto.ReqWellness:
		s.reply(proto.RespWellness, byte(s.Wellness>>8), byte(s.Wellness&0xFF))
	case len(p) == 3 && p[0] == proto.CmdReq && p[1] == proto.ReqD21mStat:
		slot := int(p[2])
		if slot >= 12 {
			s.reply(proto.RespError, proto.ErrD21m2)
			return
		}
		sl := s.Slots[slot]
		flags := byte(sl.Type>>4) & 3
		if sl.I8S {
			flags |= 8
		}
		if sl.VoltageOK || sl.Type == NoCard {
			flags |= 4
		}
		rs := slot
		if v, ok := s.ReportSlot[slot]; ok {
			rs = v
		}
		r := []byte{proto.RespD21mStat, byte(rs), flags, byte(sl.Type & 0x0F), byte(sl.AudioIn), byte(sl.AudioOut),
			byte(len(sl.Command) >> 4), byte(len(sl.Command) & 0xF), byte(len(sl.Status) >> 4), byte(len(sl.Status) & 0xF)}
		for _, b := range sl.Status {
			r = append(r, b>>4, b&0xF)
		}
		s.reply(r...)
	case len(p) == 3 && p[0] == proto.CmdReq && p[1] == proto.ReqD21mCmd:
		slot := int(p[2])
		if slot >= 12 || s.Slots[slot].Type == NoCard {
			s.reply(proto.RespError, proto.ErrD21m2)
			return
		}
		r := []byte{proto.RespD21mCmd, byte(slot)}
		for _, b := range s.Slots[slot].Command {
			r = append(r, b>>4, b&0xF)
		}
		s.reply(r...)
	case len(p) >= 3 && p[0] == proto.CmdSet && p[1] == proto.ReqD21mCmd:
		slot := int(p[2])
		cmd := proto.DecodeNibbles(append([]byte{0, 0, 0}, p[3:]...), 3, (len(p)-3)/2)
		if slot < 12 && s.Slots[slot].Type != NoCard {
			if len(cmd) == len(s.Slots[slot].Command) {
				s.Slots[slot].Command = cmd
			}
			s.Received[slot] = cmd
		}
		// SET has no answer
	default:
		s.reply(proto.RespError, proto.ErrUnknown&0x7F)
	}
}

func (s *SimPort) ReadTimeout(p []byte, d time.Duration) (int, error) {
	deadline := time.Now().Add(d)
	for {
		s.mu.Lock()
		if len(s.out) > 0 {
			n := copy(p, s.out)
			s.out = s.out[n:]
			s.mu.Unlock()
			return n, nil
		}
		s.mu.Unlock()
		left := time.Until(deadline)
		if left <= 0 {
			return 0, nil
		}
		select {
		case <-s.cond:
		case <-time.After(left):
		}
	}
}

func (s *SimPort) Flush() {
	s.mu.Lock()
	s.out = nil
	s.mu.Unlock()
}

func (s *SimPort) Close() error { return nil }

// Mutate lets tests/demo change the simulated hardware safely.
func (s *SimPort) Mutate(f func(s *SimPort)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f(s)
}
