// SPDX-License-Identifier: AGPL-3.0-or-later
package midi

import (
	"bytes"
	"reflect"
	"testing"
)

func TestParser(t *testing.T) {
	var p Parser
	// CC with running status split across packets, a clock byte in the middle,
	// a SysEx block split across packets, note on velocity 0, pitch bend
	in := [][]byte{{0xB2, 7}, {100, 0xF8, 8, 50}, {0xF0, 0x7D, 0x01}, {0x02, 0xF7}, {0x91, 60, 0}, {0xE0, 0x00, 0x40}}
	var got []Msg
	for _, b := range in {
		got = append(got, p.Feed(b)...)
	}
	want := []Msg{
		{Kind: CC, Ch: 2, Num: 7, Val: 100},
		{Kind: CC, Ch: 2, Num: 8, Val: 50},
		{Kind: SysEx, Data: []byte{0x7D, 0x01, 0x02}},
		{Kind: NoteOff, Ch: 1, Num: 60},
		{Kind: PitchBend, Val: 8192},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v\nwant %v", got, want)
	}
	if b := (Msg{Kind: PitchBend, Ch: 3, Val: 16383}).Bytes(); b[0] != 0xE3 || b[1] != 0x7F || b[2] != 0x7F {
		t.Fatalf("% X", b)
	}
	if b := (Msg{Kind: SysEx, Data: []byte{0x7D, 5}}).Bytes(); !bytes.Equal(b, []byte{0xF0, 0x7D, 5, 0xF7}) {
		t.Fatalf("% X", b)
	}
	// an overlong SysEx is dropped, parsing continues
	long := append([]byte{0xF0}, make([]byte, MaxSysEx+10)...)
	long = append(long, 0xF7, 0xB0, 1, 2)
	if got := p.Feed(long); len(got) != 1 || got[0].Kind != CC {
		t.Fatalf("after long sysex: %v", got)
	}
}
