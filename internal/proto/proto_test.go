// SPDX-License-Identifier: AGPL-3.0-or-later
package proto

import (
	"bytes"
	"testing"
)

// The slot-status request is the fixed frame
// 80 01 50 00 00 and patches [3]=slot, [4]=0xEE-slot.
func TestSlotStatusFrame(t *testing.T) {
	for slot := 0; slot < 12; slot++ {
		want := []byte{0x80, 0x01, 0x50, byte(slot), byte(0xEE - slot)}
		if got := SlotStatusRequest(slot); !bytes.Equal(got, want) {
			t.Fatalf("slot %d: got % X want % X", slot, got, want)
		}
	}
}

func TestChecksumFormula(t *testing.T) {
	// GetWellness: data = {1, 120}; frame = {0x80|addr, 1, 120, cks}
	got := WellnessRequest(0)
	want := []byte{0x80, 0x01, 0x78, byte(((255 - (1 + 120)) | 0xC0) & 0xFF)}
	if !bytes.Equal(got, want) {
		t.Fatalf("got % X want % X", got, want)
	}
	if !IsTerminator(got[len(got)-1]) {
		t.Fatal("checksum must be a terminator byte")
	}
}

func TestSetCommandNibbles(t *testing.T) {
	f := SetCommand(3, []byte{0xA5, 0x0F})
	want := []byte{0x80, 0x02, 0x51, 0x03, 0x0A, 0x05, 0x00, 0x0F}
	want = append(want, Checksum(want[1:]))
	if !bytes.Equal(f, want) {
		t.Fatalf("got % X want % X", f, want)
	}
	// round trip through the response decoder (same layout as REQ answer)
	resp := append([]byte{0x80, 0x51, 0x03}, f[4:len(f)-1]...)
	resp = append(resp, Checksum(resp[1:]))
	if got := CommandFromResponse(resp); !bytes.Equal(got, []byte{0xA5, 0x0F}) {
		t.Fatalf("decode got % X", got)
	}
}

func TestParseSlotStatus(t *testing.T) {
	// type 0x16 (MicEven): low nibble 6 in [4], bits 4-5 (=1) in [3] low bits
	resp := []byte{0x80, 0x50, 0x02, 0x04 | 0x01, 0x06, 4, 0, 0, 8, 0, 1, 0x0A, 0x03}
	resp = append(resp, Checksum(resp[1:]))
	s, err := ParseSlotStatus(resp)
	if err != nil {
		t.Fatal(err)
	}
	if s.CardType != 0x16 || !s.VoltageOK || s.I8S || s.CommandSize != 8 || s.StatusSize != 1 || s.Status[0] != 0xA3 {
		t.Fatalf("bad decode %+v", s)
	}
}
