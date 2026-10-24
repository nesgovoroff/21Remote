// SPDX-License-Identifier: AGPL-3.0-or-later
// Package midi parses and builds MIDI 1.0 channel messages.
package midi

import (
	"fmt"
)

// Kinds of channel messages the app uses.
const (
	NoteOff   = 0x80
	NoteOn    = 0x90
	CC        = 0xB0
	Program   = 0xC0
	PitchBend = 0xE0
	SysEx     = 0xF0
)

// MaxSysEx is the longest system exclusive message kept (longer ones are dropped).
const MaxSysEx = 16384

// Msg is one channel message. Ch is 0-15, Num the note/controller number,
// Val the velocity/value (0-127, or 0-16383 for pitch bend). A SysEx message
// carries its bytes between F0 and F7 in Data.
type Msg struct {
	Kind int
	Ch   int
	Num  int
	Val  int
	Data []byte
}

func (m Msg) Bytes() []byte {
	if m.Kind == SysEx {
		return append(append([]byte{0xF0}, m.Data...), 0xF7)
	}
	st := byte(m.Kind | (m.Ch & 0x0F))
	switch m.Kind {
	case Program:
		return []byte{st, byte(m.Num & 0x7F)}
	case PitchBend:
		return []byte{st, byte(m.Val & 0x7F), byte(m.Val >> 7 & 0x7F)}
	}
	return []byte{st, byte(m.Num & 0x7F), byte(m.Val & 0x7F)}
}

func (m Msg) String() string {
	switch m.Kind {
	case CC:
		return fmt.Sprintf("Ch %d CC %d = %d", m.Ch+1, m.Num, m.Val)
	case NoteOn:
		return fmt.Sprintf("Ch %d Note %d on, vel %d", m.Ch+1, m.Num, m.Val)
	case NoteOff:
		return fmt.Sprintf("Ch %d Note %d off", m.Ch+1, m.Num)
	case PitchBend:
		return fmt.Sprintf("Ch %d Pitch bend = %d", m.Ch+1, m.Val)
	case Program:
		return fmt.Sprintf("Ch %d Program %d", m.Ch+1, m.Num+1)
	case SysEx:
		return fmt.Sprintf("SysEx, %d bytes", len(m.Data)+2)
	}
	return fmt.Sprintf("Ch %d status %02X", m.Ch+1, m.Kind)
}

// Parser turns a raw byte stream (one per source) into messages, handling
// running status, collecting SysEx and skipping real-time bytes.
type Parser struct {
	status byte
	data   []byte
	sysex  bool
	sx     []byte
	sxBad  bool
}

func dataLen(st byte) int {
	switch st & 0xF0 {
	case 0xC0, 0xD0:
		return 1
	}
	return 2
}

// Feed consumes bytes and returns complete channel messages.
// Note On with velocity 0 is reported as Note Off.
func (p *Parser) Feed(b []byte) []Msg {
	var out []Msg
	for _, x := range b {
		switch {
		case x >= 0xF8: // real-time: ignore, does not affect running status
			continue
		case x == 0xF0:
			p.sysex = true
			p.status = 0
			p.sx, p.sxBad = p.sx[:0], false
			continue
		case x == 0xF7:
			if p.sysex && !p.sxBad {
				out = append(out, Msg{Kind: SysEx, Data: append([]byte(nil), p.sx...)})
			}
			p.sysex = false
			continue
		case x >= 0xF0: // other system common: cancel running status
			p.status = 0
			p.data = p.data[:0]
			continue
		case x >= 0x80:
			p.sysex = false
			p.status = x
			p.data = p.data[:0]
			continue
		}
		if p.sysex {
			if len(p.sx) < MaxSysEx {
				p.sx = append(p.sx, x)
			} else {
				p.sxBad = true
			}
			continue
		}
		if p.status == 0 {
			continue
		}
		p.data = append(p.data, x)
		if len(p.data) < dataLen(p.status) {
			continue
		}
		m := Msg{Kind: int(p.status & 0xF0), Ch: int(p.status & 0x0F)}
		switch m.Kind {
		case Program, 0xD0:
			m.Num = int(p.data[0])
		case PitchBend:
			m.Val = int(p.data[0]) | int(p.data[1])<<7
		default:
			m.Num, m.Val = int(p.data[0]), int(p.data[1])
		}
		if m.Kind == NoteOn && m.Val == 0 {
			m.Kind = NoteOff
		}
		p.data = p.data[:0]
		if m.Kind == NoteOn || m.Kind == NoteOff || m.Kind == CC || m.Kind == PitchBend || m.Kind == Program {
			out = append(out, m)
		}
	}
	return out
}
