// SPDX-License-Identifier: AGPL-3.0-or-later
package core

import (
	"fmt"
	"strings"
)

// Params is an ordered, case-insensitive string map.
type Params struct {
	keys []string
	vals map[string]string // lower(key) -> value
	orig map[string]string // lower(key) -> key as first written
}

func NewParams() *Params {
	return &Params{vals: map[string]string{}, orig: map[string]string{}}
}

func (p *Params) Get(k string) (string, bool) {
	v, ok := p.vals[strings.ToLower(k)]
	return v, ok
}

func (p *Params) Set(k, v string) {
	l := strings.ToLower(k)
	if _, ok := p.vals[l]; !ok {
		p.keys = append(p.keys, l)
		p.orig[l] = k
	}
	p.vals[l] = v
}

func (p *Params) Delete(k string) {
	l := strings.ToLower(k)
	if _, ok := p.vals[l]; !ok {
		return
	}
	delete(p.vals, l)
	delete(p.orig, l)
	for i, x := range p.keys {
		if x == l {
			p.keys = append(p.keys[:i], p.keys[i+1:]...)
			break
		}
	}
}

// Each iterates in insertion order with the key spelling as first written.
func (p *Params) Each(f func(k, v string)) {
	for _, l := range p.keys {
		f(p.orig[l], p.vals[l])
	}
}

// Card is one card of the frame.
type Card struct {
	Slot        int
	Type        int
	VoltageOK   bool
	I8S         bool
	AudioIn     int
	AudioOut    int
	CommandSize int
	StatusSize  int
	Command     []byte
	Status      []byte
	Show        bool
	Params      *Params
	InputID     int
	OutputID    int
}

func newCard(slot, typ int) *Card {
	return &Card{Slot: slot, Type: typ, VoltageOK: true, Show: true, Params: NewParams()}
}

func (c *Card) setAudioIn(v int) {
	c.AudioIn = v
	if IsMicType(c.Type) {
		c.AudioIn = 4
	}
}

func (c *Card) setType(t int) {
	c.Type = t
	if IsMicType(t) {
		c.AudioIn = 4
	}
}

// setCommandSize reallocates the command field when the size changes.
func (c *Card) setCommandSize(n int) {
	if n != c.CommandSize || len(c.Command) != n {
		c.CommandSize = n
		c.Command = make([]byte, n)
	}
}

func (c *Card) setStatusSize(n int) {
	c.StatusSize = n
	if len(c.Status) != n {
		c.Status = make([]byte, n)
	}
}

func (c *Card) bit(byteIdx, bit int) bool {
	if byteIdx < 0 || byteIdx >= len(c.Command) {
		return false
	}
	return c.Command[byteIdx]&(1<<uint(bit)) != 0
}

func (c *Card) setBit(byteIdx, bit int, v bool) {
	if byteIdx < 0 || byteIdx >= len(c.Command) {
		return
	}
	if v {
		c.Command[byteIdx] |= 1 << uint(bit)
	} else {
		c.Command[byteIdx] &^= 1 << uint(bit)
	}
}

func (c *Card) cmdByte(i int) byte {
	if i < 0 || i >= len(c.Command) {
		return 0
	}
	return c.Command[i]
}

func (c *Card) setByte(i int, v byte) {
	if i >= 0 && i < len(c.Command) {
		c.Command[i] = v
	}
}

func (c *Card) statByte(i int) byte {
	if i < 0 || i >= len(c.Status) {
		return 0
	}
	return c.Status[i]
}

// CommandString is the hex form stored in snapshots (D21mCard.CommandString).
func (c *Card) CommandString() string {
	var b strings.Builder
	for _, x := range c.Command {
		fmt.Fprintf(&b, "%02X", x)
	}
	return b.String()
}

// Channels is the number of audio channels in and out: the frame counts in
// pairs (I2S) or in groups of 8 (I8S); microphone cards report channels.
func (c *Card) Channels() (in, out int) {
	if IsMicType(c.Type) {
		return c.AudioIn, c.AudioOut
	}
	return c.AudioIn * c.mult(), c.AudioOut * c.mult()
}

func (c *Card) mult() int {
	if c.I8S {
		return 8
	}
	return 2
}

// Rack is one D21m frame with 12 slots.
type Rack struct {
	Label    string
	Online   bool
	Module   int
	Wellness int
	Cards    [12]*Card
}

func NewRack(label string) *Rack {
	r := &Rack{Label: label, Online: true, Module: 0xFFFF}
	for i := range r.Cards {
		r.Cards[i] = newCard(i, NoCard)
	}
	return r
}

// UpdateChannelIds numbers the audio channels of all cards, with the
// even/odd microphone card pairing rule.
func (r *Rack) UpdateChannelIds() {
	in, out := 1, 1
	prevMicEven := false
	for _, c := range r.Cards {
		c.InputID = in
		c.OutputID = out
		if IsMicType(c.Type) {
			if isMicEven(c.Type) {
				prevMicEven = true
				in += 8
			} else if prevMicEven {
				c.InputID = in - 4
				prevMicEven = false
			}
			continue
		}
		prevMicEven = false
		in += c.AudioIn * c.mult()
		out += c.AudioOut * c.mult()
	}
}
