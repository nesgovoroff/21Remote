// SPDX-License-Identifier: AGPL-3.0-or-later
// Package proto implements the D21m host<->module serial protocol.
//
// Wire format (115200 8N1, no handshake, DTR/RTS off):
//
//	host -> module:  [0x80|addr] [payload...] [checksum]
//	module -> host:  [start] [cmd] [data...] [checksum]
//
// Payload/data bytes are 7-bit. Multi-byte values are sent as nibbles
// (high nibble first). The checksum is computed over the payload (everything
// after the start byte): ((0xFF - sum) | 0xC0) & 0xFF, so a frame always ends
// with a byte whose two top bits are set; the receiver uses this as the
// end-of-frame marker.
package proto

import (
	"fmt"
)

// Host-to-module command classes.
const (
	CmdReq     = 0x01
	CmdSet     = 0x02
	CmdSetLev  = 0x03
	CmdSetGrm  = 0x04
	CmdService = 0x08
)

// Request / response sub-commands used by the remote software.
const (
	ReqModType  = 0x01
	ReqFwVer    = 0x02
	ReqD21mStat = 0x50
	ReqD21mCmd  = 0x51
	ReqWellness = 0x78

	RespModType  = 0x01
	RespD21mStat = 0x50
	RespD21mCmd  = 0x51
	RespWellness = 0x78
	RespError    = 0x7F
)

// Checksum returns the D21m checksum of data (all bytes after the start byte).
func Checksum(data []byte) byte {
	sum := 0
	for _, b := range data {
		sum += int(b)
	}
	return byte((0xFF - sum) | 0xC0)
}

// IsTerminator reports whether b can end a frame.
func IsTerminator(b byte) bool { return b&0xC0 == 0xC0 }

// IsStart reports whether b is a frame start byte (MSB set, not a terminator).
func IsStart(b byte) bool { return b&0xC0 == 0x80 }

func frame(addr byte, payload ...byte) []byte {
	f := make([]byte, 0, len(payload)+2)
	f = append(f, 0x80|addr)
	f = append(f, payload...)
	return append(f, Checksum(payload))
}

// ModuleTypeRequest asks module at addr for its module type.
func ModuleTypeRequest(addr byte) []byte { return frame(addr, CmdReq, ReqModType) }

// WellnessRequest asks module at addr for its wellness word.
func WellnessRequest(addr byte) []byte { return frame(addr, CmdReq, ReqWellness) }

// SlotStatusRequest asks the D21m frame (addr 0) for the status of a slot.
// The frame is {0x80, 0x01, 0x50, slot, 0xEE-slot}.
func SlotStatusRequest(slot int) []byte { return frame(0, CmdReq, ReqD21mStat, byte(slot)) }

// CommandRequest asks for the current command field of a slot.
func CommandRequest(slot int) []byte { return frame(0, CmdReq, ReqD21mCmd, byte(slot)) }

// SetCommand builds the frame that writes the command field of a slot.
func SetCommand(slot int, cmd []byte) []byte {
	p := make([]byte, 0, 3+2*len(cmd))
	p = append(p, CmdSet, ReqD21mCmd, byte(slot))
	for _, b := range cmd {
		p = append(p, (b&0xF0)>>4, b&0x0F)
	}
	return frame(0, p...)
}

// Word16 decodes the 16-bit value in resp[2..3] (module type / wellness).
func Word16(resp []byte) (int, error) {
	if len(resp) < 4 {
		return 0, fmt.Errorf("short response (%d bytes)", len(resp))
	}
	return int(resp[2])<<8 | int(resp[3]), nil
}

// DecodeNibbles decodes count bytes from nibble pairs starting at resp[off].
func DecodeNibbles(resp []byte, off, count int) []byte {
	out := make([]byte, count)
	for i := 0; i < count; i++ {
		hi, lo := off+2*i, off+2*i+1
		if lo >= len(resp) {
			break
		}
		out[i] = resp[hi]<<4 | resp[lo]
	}
	return out
}

// CommandFromResponse decodes the answer to CommandRequest.
func CommandFromResponse(resp []byte) []byte {
	n := (len(resp) - 4) / 2
	if n < 0 {
		n = 0
	}
	return DecodeNibbles(resp, 3, n)
}

// SlotStatus is the decoded answer to SlotStatusRequest.
type SlotStatus struct {
	Slot        int
	CardType    int
	I8S         bool
	VoltageOK   bool
	AudioIn     int
	AudioOut    int
	CommandSize int
	StatusSize  int
	Status      []byte
}

// ParseSlotStatus decodes a D21M_STAT response.
func ParseSlotStatus(resp []byte) (SlotStatus, error) {
	var s SlotStatus
	if len(resp) < 11 {
		return s, fmt.Errorf("short slot status (%d bytes)", len(resp))
	}
	s.Slot = int(resp[2])
	s.CardType = int(resp[4]) | int(resp[3]&3)<<4
	s.I8S = resp[3]&8 != 0
	s.VoltageOK = resp[3]&4 != 0
	s.AudioIn = int(resp[5])
	s.AudioOut = int(resp[6])
	s.CommandSize = int(resp[7])<<4 | int(resp[8])
	s.StatusSize = int(resp[9])<<4 | int(resp[10])
	s.Status = DecodeNibbles(resp, 11, s.StatusSize)
	return s, nil
}

// Error codes reported in an error response (resp[1]==0x7F, code in resp[2]).
const (
	ErrChecksum   = 0x00
	ErrD21m2      = 0x52
	ErrD21m4      = 0x54
	ErrD21m5      = 0x55
	ErrUnknown    = 0xFF
	WellnessSick  = 64 // wellness bit: invalid configuration
	ModuleNone    = 0xFFFF
	ModuleLVDS    = 0x50
	ModuleMADI    = 0x51
	SlotsPerFrame = 12
)

// DeviceError is an error frame returned by the module.
type DeviceError struct{ Code int }

func (e *DeviceError) Error() string {
	switch e.Code {
	case ErrChecksum:
		return "Checksum ERROR"
	case ErrD21m2:
		return "D21mError_2, no card present in accessed slot!"
	case ErrD21m4:
		return "D21mError_4, maybe reconfigure will help out"
	case ErrD21m5:
		return "D21mError_5, invalid command length"
	}
	return fmt.Sprintf("Unknown D21m ERROR (0x%02X)", e.Code)
}

// CheckError returns a *DeviceError if resp is an error frame.
func CheckError(resp []byte) error {
	if len(resp) >= 2 && resp[1] == RespError {
		code := ErrUnknown
		if len(resp) >= 3 {
			code = int(resp[2])
		}
		return &DeviceError{Code: code}
	}
	return nil
}

// ModuleDescription names the module type.
func ModuleDescription(t int) string {
	switch t {
	case ModuleLVDS:
		return "D21m LVDS HD"
	case ModuleMADI:
		return "D21m MADI HD"
	}
	return "Unknown Module"
}
