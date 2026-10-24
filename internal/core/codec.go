// SPDX-License-Identifier: AGPL-3.0-or-later
package core

// Command-field codecs for each card type, reconstructed bit-for-bit from the
// channel strips.

// MICLevel maps mic gain (-11..+75 dB, index = gain+11) to the 13-bit
// gain code of the microphone card (MicComponent.MICLevel).
var MICLevel = [87]int{
	0x1042, 0x104a, 0x1046, 0x104e, 0x1060, 0x1068, 0x1064, 0x106c, 0x1062, 0x106a, 0x1066, 0x106e,
	0x1160, 0x1168, 0x1164, 0x116c, 0x1162, 0x116a, 0x1166, 0x116e, 0x10e0, 0x10e8, 0x10e4, 0x10ed,
	0x10e3, 0x10eb, 0x10e7, 0x10ef, 0x10d1, 0x10d9, 0x10d5, 0x10dd, 0x10d3, 0xdb, 0xd7, 0xdf, 0xd,
	0x3, 0xb, 0x7, 0xf, 0x21, 0x29, 0x25, 0x2d, 0x23, 0x2b, 0x27, 0x2f, 0x121, 0x129, 0x125, 0x12d,
	0x123, 0x12b, 0x127, 0x12f, 0xa1, 0xa9, 0xa5, 0xad, 0xa3, 0xab, 0xa7, 0xaf, 0x91, 0x99, 0x95,
	0x9d, 0x93, 0x9b, 0x97, 0x9f, 0x191, 0x199, 0x195, 0x19d, 0x193, 0x19b, 0x197, 0x19f, 0x1b1,
	0x1b9, 0x1b5, 0x1bd, 0x1b3, 0x1bb,
}

const (
	MicGainMin, MicGainMax, MicRangeLimit = -11, 75, 21
	DL5GainMin, DL5GainMax, DL5RangeLimit = -13, 62, 17
	MicCommandSize                        = 8
	DL5CommandSize                        = 16
)

func reverseByte(b byte) byte {
	var r byte
	for i := 0; i < 8; i++ {
		if b&(1<<uint(i)) != 0 {
			r |= 1 << uint(7-i)
		}
	}
	return r
}

// ---- 4-channel microphone card (MicComponent) ----

type MicState struct {
	Gain     int  `json:"gain"`
	Phantom  bool `json:"phantom"`
	HPF      bool `json:"hpf"`
	Insert   bool `json:"insert"`
	Softclip bool `json:"softclip"`
}

func micBase(ch int) int { return (4 - ch) * 2 }

// micDecode returns the state of channel ch (1..4). prevGain is kept when the
// gain code is not in the table (the fader is left untouched).
func micDecode(c *Card, ch, prevGain int) MicState {
	b := micBase(ch)
	s := MicState{
		Phantom:  c.bit(b+1, 6),
		HPF:      c.bit(b+1, 7),
		Insert:   c.bit(b+1, 4),
		Softclip: c.bit(b+1, 2),
		Gain:     prevGain,
	}
	lvl := int(reverseByte(c.cmdByte(b))) << 1
	if c.bit(b+1, 5) {
		lvl |= 0x1000
	}
	if c.bit(b+1, 0) {
		lvl |= 1
	}
	if lvl == 0 {
		s.Gain = MicGainMin
	} else {
		for i, v := range MICLevel {
			if v == lvl {
				s.Gain = i + MicGainMin
				break
			}
		}
	}
	return s
}

func micEncode(c *Card, ch int, s MicState) {
	if c.CommandSize != MicCommandSize {
		return
	}
	b := micBase(ch)
	g := clamp(s.Gain, MicGainMin, MicGainMax)
	lvl := MICLevel[g-MicGainMin]
	c.setByte(b, reverseByte(byte((0x1FE&lvl)>>1)))
	c.setBit(b+1, 5, lvl&0x1000 != 0)
	c.setBit(b+1, 0, lvl&1 != 0)
	c.setBit(b+1, 6, s.Phantom)
	c.setBit(b+1, 7, s.HPF)
	c.setBit(b+1, 3, !s.Insert)
	c.setBit(b+1, 4, s.Insert)
	c.setBit(b+1, 2, s.Softclip)
}

// ---- 8-channel DL5 microphone card (DL5MicComponent) ----

type DL5State struct {
	Gain    int  `json:"gain"`
	Phantom bool `json:"phantom"`
	HPF     bool `json:"hpf"`
	CMS     bool `json:"cms"` // common-mode servo
	DCS     bool `json:"dcs"` // DC servo
}

func dl5Decode(c *Card, ch int) DL5State {
	b := (ch - 1) * 2
	v := 0
	if !c.bit(b, 6) {
		v += 20
	}
	v += int(reverseByte(c.cmdByte(b+1))) - 1
	return DL5State{
		Gain:    clamp(v+DL5GainMin, DL5GainMin, DL5GainMax),
		Phantom: c.bit(b, 7),
		HPF:     c.bit(b, 5),
		CMS:     c.bit(b, 1),
		DCS:     !c.bit(b, 0),
	}
}

func dl5Encode(c *Card, ch int, s DL5State) {
	if c.CommandSize != DL5CommandSize {
		return
	}
	b := (ch - 1) * 2
	v := clamp(s.Gain, DL5GainMin, DL5GainMax) - DL5GainMin
	if v < 30 {
		c.setBit(b, 6, true)
		c.setByte(b+1, reverseByte(byte(v+1)))
	} else {
		c.setBit(b, 6, false)
		c.setByte(b+1, reverseByte(byte(v+1-20)))
	}
	c.setBit(b, 7, s.Phantom)
	c.setBit(b, 5, s.HPF)
	c.setBit(b, 1, s.CMS)
	c.setBit(b, 0, !s.DCS)
}

// ---- AES/EBU cards (AESEBUComponent) ----

// Sample-rate choices for the output SFC groups, in UI order.
var FsOutNames = []string{"44.1", "48", "96", "ext"}

// Output word-length choices, in UI order.
var WordlengthNames = []string{"16-bit", "18-bit", "20-bit", "24-bit"}

type AESState struct {
	Lock       [8]bool `json:"lock"`       // input locked
	ExtLock    bool    `json:"extLock"`    // IOSFC only
	HasSFCIn   bool    `json:"hasSfcIn"`   // ISFC/IOSFC
	HasOut     bool    `json:"hasOut"`     // IOSFC
	SFCIn      [8]bool `json:"sfcIn"`      // SFC enabled per input
	SFCOut     [2]bool `json:"sfcOut"`     // out 1-4, out 5-8
	FsOut      [2]int  `json:"fsOut"`      // index into FsOutNames
	Wordlength [8]int  `json:"wordlength"` // index into WordlengthNames
}

func aesSfcInByte(c *Card) int {
	if c.Type == AESEBUCardIOSFC {
		return 3
	}
	return 0
}

func aesDecode(c *Card) AESState {
	var s AESState
	if c.StatusSize >= 2 {
		st0, st1 := c.statByte(0), c.statByte(1)
		for i := 0; i < 8; i++ {
			s.Lock[i] = st0&(1<<uint(i)) == 0
		}
		s.ExtLock = st1&1 == 0
	}
	if c.Type == AESEBUCard {
		return s
	}
	s.HasSFCIn = true
	sb := aesSfcInByte(c)
	for i := 0; i < 8; i++ {
		s.SFCIn[i] = !c.bit(sb, i)
	}
	if c.Type != AESEBUCardIOSFC {
		return s
	}
	s.HasOut = true
	s.SFCOut[0] = !c.bit(2, 0)
	s.SFCOut[1] = !c.bit(2, 1)
	for g := 0; g < 2; g++ {
		v := 0
		if c.bit(2, 2+g*2) {
			v = 2
		}
		if c.bit(2, 3+g*2) {
			if v == 2 {
				v = 3
			}
		} else if v == 0 {
			v = 1
		}
		s.FsOut[g] = v
	}
	for ch := 0; ch < 8; ch++ {
		byteIdx := 0
		if ch < 4 {
			byteIdx = 1
		}
		w := 0
		if c.bit(byteIdx, (ch%4)*2) {
			w |= 1
		}
		if c.bit(byteIdx, (ch%4)*2+1) {
			w |= 2
		}
		s.Wordlength[ch] = 3 - w
	}
	return s
}

func aesSetSFCIn(c *Card, i int, on bool) { c.setBit(aesSfcInByte(c), i, !on) }

func aesSetSFCOut(c *Card, g int, on bool) { c.setBit(2, g, !on) }

// FS out encoding (bit 2+2g, bit 3+2g): 44.1=(0,1) 48=(0,0) 96=(1,0) ext=(1,1)
var fsOutBits = [4][2]bool{{false, true}, {false, false}, {true, false}, {true, true}}

func aesSetFsOut(c *Card, g, v int) {
	v = clamp(v, 0, 3)
	c.setBit(2, 2+g*2, fsOutBits[v][0])
	c.setBit(2, 3+g*2, fsOutBits[v][1])
}

func aesSetWordlength(c *Card, ch, k int) {
	byteIdx := 0
	if ch < 4 {
		byteIdx = 1
	}
	w := 3 - k
	c.setBit(byteIdx, (ch%4)*2, w&1 != 0)
	c.setBit(byteIdx, (ch%4)*2+1, w&2 != 0)
}

// ---- ADAT (ADATComponent) ----

type ADATState struct {
	Lock     [2]bool `json:"lock"`
	TwoBanks bool    `json:"twoBanks"`
}

func adatDecode(c *Card) ADATState {
	st := c.statByte(0)
	return ADATState{
		Lock:     [2]bool{st&1 == 0, st&2 == 0},
		TwoBanks: c.AudioIn*c.mult() != 8,
	}
}

// ---- GPIO (GPIOComponent) ----

type GPIOState struct {
	GPI [16]bool `json:"gpi"`
	GPO [16]bool `json:"gpo"`
}

func gpioDecode(c *Card) GPIOState {
	var s GPIOState
	for i := 0; i < 16; i++ {
		s.GPO[i] = c.bit(i/8, i%8)
		s.GPI[i] = c.statByte(i/8)&(1<<uint(i%8)) != 0
	}
	return s
}

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
