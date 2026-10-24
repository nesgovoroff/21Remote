// SPDX-License-Identifier: AGPL-3.0-or-later
package core

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestReverseByte(t *testing.T) {
	if reverseByte(0x01) != 0x80 || reverseByte(0xF0) != 0x0F || reverseByte(0xA5) != 0xA5 {
		t.Fatal("reverseByte")
	}
}

func TestMicCodecRoundTrip(t *testing.T) {
	c := &Card{CommandSize: 8, Command: make([]byte, 8)}
	for ch := 1; ch <= 4; ch++ {
		for g := MicGainMin; g <= MicGainMax; g++ {
			in := MicState{Gain: g, Phantom: g%2 == 0, HPF: g%3 == 0, Insert: g%5 == 0, Softclip: g%7 == 0}
			micEncode(c, ch, in)
			if out := micDecode(c, ch, 999); out != in {
				t.Fatalf("ch %d gain %d: %+v != %+v", ch, g, out, in)
			}
			// insert writes its complement into bit 3 (InsertCheck_CheckedChanged)
			if c.bit(micBase(ch)+1, 3) == in.Insert {
				t.Fatal("insert complement bit")
			}
		}
	}
	// channel 4 lives in bytes 0/1, channel 1 in bytes 6/7
	c2 := &Card{CommandSize: 8, Command: make([]byte, 8)}
	micEncode(c2, 4, MicState{Gain: 75, Phantom: true})
	if c2.Command[6] != 0 || c2.Command[1]&0x40 == 0 {
		t.Fatalf("channel layout % X", c2.Command)
	}
}

func TestMicLevelTableUnique(t *testing.T) {
	seen := map[int]bool{}
	for _, v := range MICLevel {
		if seen[v] {
			t.Fatalf("duplicate level 0x%x", v)
		}
		seen[v] = true
	}
}

func TestDL5CodecRoundTrip(t *testing.T) {
	c := &Card{CommandSize: 16, Command: make([]byte, 16)}
	for ch := 1; ch <= 8; ch++ {
		for g := DL5GainMin; g <= DL5GainMax; g++ {
			in := DL5State{Gain: g, Phantom: g%2 == 0, HPF: g%3 == 0, CMS: g%4 == 0, DCS: g%5 == 0}
			dl5Encode(c, ch, in)
			if out := dl5Decode(c, ch); out != in {
				t.Fatalf("ch %d gain %d: %+v != %+v", ch, g, out, in)
			}
		}
	}
}

func TestAESCodec(t *testing.T) {
	c := &Card{Type: AESEBUCardIOSFC, CommandSize: 4, Command: make([]byte, 4), StatusSize: 2, Status: []byte{0x0F, 0x00}}
	for g := 0; g < 2; g++ {
		for v := 0; v < 4; v++ {
			aesSetFsOut(c, g, v)
			if aesDecode(c).FsOut[g] != v {
				t.Fatalf("fsOut g%d v%d", g, v)
			}
		}
	}
	for ch := 0; ch < 8; ch++ {
		for k := 0; k < 4; k++ {
			aesSetWordlength(c, ch, k)
			if aesDecode(c).Wordlength[ch] != k {
				t.Fatalf("wordlength ch%d k%d", ch, k)
			}
		}
	}
	for i := 0; i < 8; i++ {
		aesSetSFCIn(c, i, false)
	}
	aesSetSFCOut(c, 0, false)
	aesSetSFCIn(c, 2, true)
	aesSetSFCOut(c, 1, true)
	s := aesDecode(c)
	if !s.SFCIn[2] || s.SFCIn[3] || !s.SFCOut[1] || s.SFCOut[0] {
		t.Fatalf("sfc %+v", s)
	}
	if s.Lock[0] || !s.Lock[4] || !s.ExtLock {
		t.Fatalf("lock %+v", s)
	}
	// ISFC stores SFC-in in byte 0
	c5 := &Card{Type: AESEBUCardISFC, CommandSize: 1, Command: []byte{0xFF}}
	aesSetSFCIn(c5, 0, true)
	if c5.Command[0] != 0xFE {
		t.Fatalf("isfc % X", c5.Command)
	}
}

func TestChannelIDs(t *testing.T) {
	r := NewRack("R")
	r.Cards[0].setType(MicEven)
	r.Cards[1].setType(MicOdd)
	r.Cards[2].setType(LineInCard)
	r.Cards[2].AudioIn = 4
	r.Cards[3].setType(ADAT16)
	r.Cards[3].AudioIn, r.Cards[3].I8S = 2, true
	r.UpdateChannelIds()
	if r.Cards[0].InputID != 1 || r.Cards[1].InputID != 5 || r.Cards[2].InputID != 9 || r.Cards[3].InputID != 17 {
		t.Fatalf("ids %d %d %d %d", r.Cards[0].InputID, r.Cards[1].InputID, r.Cards[2].InputID, r.Cards[3].InputID)
	}
}

func newTestApp(t *testing.T) (*App, *SimPort) {
	dir := t.TempDir()
	cfg := LoadConfig(filepath.Join(dir, "settings.xml"))
	cfg.Set("COM_Port", DemoPort)
	a := NewApp(cfg)
	a.mu.Lock()
	a.openPortLocked(DemoPort)
	a.mu.Unlock()
	go a.senderLoop()
	return a, a.Sim()
}

func TestPollAgainstSimulator(t *testing.T) {
	a, sim := newTestApp(t)
	a.Poll()
	st := a.State()
	if !st.Online || st.Rack.Module != "D21m LVDS HD" || st.Rack.Wellness != "Healthy" {
		t.Fatalf("rack %+v", st.Rack)
	}
	kinds := map[string]int{}
	for _, s := range st.Strips {
		kinds[s.Kind]++
	}
	if kinds[KindMic] != 8 || kinds[KindDL5] != 8 || kinds[KindAES] != 1 || kinds[KindGPIO] != 1 || kinds[KindADAT] != 1 || kinds[KindGeneric] != 1 {
		t.Fatalf("strips %v", kinds)
	}
	// command fields were read from the hardware
	var mic1 *StripView
	for i := range st.Strips {
		if st.Strips[i].ID == "s0c1" {
			mic1 = &st.Strips[i]
		}
	}
	if mic1 == nil || mic1.Mic.Gain != 25 || !mic1.Mic.Phantom {
		t.Fatalf("mic1 %+v", mic1)
	}
	if mic1.IDs != "1/-" || mic1.Fixed != "MIC 1/1" {
		t.Fatalf("labels %q %q", mic1.IDs, mic1.Fixed)
	}

	// change a gain and check the frame reaches the simulated hardware
	if _, err := a.Do(Op{Op: "mic", Strip: "s0c1", Field: "gain", Value: float64(40)}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		var got []byte
		sim.Mutate(func(s *SimPort) { got = append([]byte(nil), s.Slots[0].Command...) })
		tmp := &Card{CommandSize: 8, Command: got}
		if micDecode(tmp, 1, 0).Gain == 40 {
			goto ok
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("gain change not sent")
ok:
	// card removal and insertion are detected on the next poll
	sim.Mutate(func(s *SimPort) { s.Slots[5] = SimSlot{} })
	a.Poll()
	if a.State().Slots[5].Type != NoCard {
		t.Fatal("removal not detected")
	}
	if !strings.Contains(a.State().Status, "removed") {
		t.Fatalf("status %q", a.State().Status)
	}
}

func TestStereoLinkMirrors(t *testing.T) {
	a, _ := newTestApp(t)
	a.Poll()
	if _, err := a.Do(Op{Op: "link", Strip: "s0c1", Value: true}); err != nil {
		t.Fatal(err)
	}
	a.Do(Op{Op: "mic", Strip: "s0c1", Field: "gain", Value: float64(12)})
	a.Do(Op{Op: "mic", Strip: "s0c1", Field: "hpf", Value: true})
	st := a.State()
	var s2 StripView
	for _, s := range st.Strips {
		if s.ID == "s0c2" {
			s2 = s
		}
	}
	if s2.Enabled || s2.Mic.Gain != 12 || !s2.Mic.HPF {
		t.Fatalf("slave %+v %+v", s2, s2.Mic)
	}
	if _, err := a.Do(Op{Op: "mic", Strip: "s0c2", Field: "gain", Value: float64(1)}); err == nil {
		t.Fatal("slave must be read-only")
	}
}

func TestSnapshotRoundTrip(t *testing.T) {
	a, _ := newTestApp(t)
	a.Poll()
	a.Do(Op{Op: "label", Strip: "s0c1", Text: "Vox & \"Lead\""})
	a.Do(Op{Op: "color", Strip: "s0c1", Text: "#123456"})
	a.Do(Op{Op: "mic", Strip: "s0c3", Field: "gain", Value: float64(60)})
	a.Do(Op{Op: "moveTo", Strip: "s0c1", Target: "s2c8", Before: false})
	path := filepath.Join(t.TempDir(), "snap.xml")
	if err := a.SaveSnapshot(path); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	if !bytes.Contains(data, []byte(`type="MicEven"`)) || !bytes.Contains(data, []byte(`key="CH1_Label"`)) {
		t.Fatalf("snapshot:\n%s", data)
	}
	// change things, then restore
	a.Do(Op{Op: "mic", Strip: "s0c3", Field: "gain", Value: float64(0)})
	a.Do(Op{Op: "label", Strip: "s0c1", Text: "x"})
	a.Do(Op{Op: "move", Strip: "s0c1", Delta: -100})
	mm, err := a.OpenSnapshot(path, false)
	if err != nil || len(mm) != 0 {
		t.Fatalf("open: %v %v", err, mm)
	}
	st := a.State()
	var prev, moved StripView
	for i, s := range st.Strips {
		if s.ID == "s0c3" && s.Mic.Gain != 60 {
			t.Fatalf("gain not restored: %d", s.Mic.Gain)
		}
		if s.ID == "s0c1" {
			moved, prev = s, st.Strips[i-1]
		}
	}
	if prev.ID != "s2c8" || moved.Label != `Vox & "Lead"` || moved.Color != "#123456" {
		t.Fatalf("order/label/colour not restored: prev=%s %+v", prev.ID, moved)
	}
}

func TestSnapshotFileFormat(t *testing.T) {
	// an indented snapshot file as stored on disk
	src := `<?xml version="1.0" encoding="utf-8"?>
<!-- This is a snapshot file for 21 Remote -->
<D21mSnapshot date="12.03.2007" time="14:02">
  <D21mSystem>
    <Rack name="Studio A" id="0">
      <Card slot="1" type="MicEven" showInChannelView="True" commandField="0102030405060708">
        <Parameter key="CH1_Color" value="-5622769" />
        <Parameter key="CH1_Label" value="Kick" />
        <Parameter key="CH1_Stereolink" value="False" />
      </Card>
      <Card slot="9" type="GPIOCard" showInChannelView="False" commandField="FF00" />
    </Rack>
  </D21mSystem>
</D21mSnapshot>`
	racks, err := ReadSnapshot(strings.NewReader(src))
	if err != nil {
		t.Fatal(err)
	}
	r := racks[0]
	if r.Label != "Studio A" || r.Cards[0].Type != MicEven || r.Cards[8].Type != GPIOCard || r.Cards[8].Show {
		t.Fatalf("%+v", r)
	}
	if v, _ := r.Cards[0].Params.Get("ch1_label"); v != "Kick" {
		t.Fatal("params")
	}
	if !bytes.Equal(r.Cards[0].Command, []byte{1, 2, 3, 4, 5, 6, 7, 8}) {
		t.Fatal("command")
	}
}

func TestSnapshotMismatch(t *testing.T) {
	a, sim := newTestApp(t)
	a.Poll()
	a.Do(Op{Op: "label", Strip: "s7c1", Text: "ADAT A"})
	path := filepath.Join(t.TempDir(), "m.xml")
	if err := a.SaveSnapshot(path); err != nil {
		t.Fatal(err)
	}
	a.Do(Op{Op: "label", Strip: "s7c1", Text: "changed"})
	sim.Mutate(func(s *SimPort) { s.Slots[5] = SimSlot{} }) // pull the line-in card
	mm, err := a.OpenSnapshot(path, false)
	if err != nil || len(mm) != 1 || mm[0].Slot != 6 || mm[0].Snapshot != "LineInCard" {
		t.Fatalf("mismatch %v %v", mm, err)
	}
	if v, _ := a.rack.Cards[7].Params.Get("CH1_Label"); v != "changed" {
		t.Fatal("must not apply before confirmation")
	}
	if _, err := a.OpenSnapshot(path, true); err != nil {
		t.Fatal(err)
	}
	if v, _ := a.rack.Cards[7].Params.Get("CH1_Label"); v != "ADAT A" {
		t.Fatalf("matching card not restored: %q", v)
	}
}

// The card list shows audio channels (pairs or groups of 8 counted out), and
// a card stays in the slot that was polled even if it reports another one.
func TestChannelsAndSlots(t *testing.T) {
	a, sim := newTestApp(t)
	sim.Mutate(func(s *SimPort) {
		s.Slots[10] = SimSlot{Type: LineOutCard, VoltageOK: true, AudioIn: 0, AudioOut: 4, Command: []byte{}, Status: []byte{}}
		s.Slots[11] = SimSlot{}
		s.ReportSlot = map[int]int{10: 11} // slot 11 answers as slot 12
	})
	a.Poll()
	st := a.State()
	if st.Slots[10].Type != LineOutCard || st.Slots[11].Type != NoCard {
		t.Fatalf("slots 11/12: %d %d", st.Slots[10].Type, st.Slots[11].Type)
	}
	if st.Slots[10].AudioOut != 8 || st.Slots[10].AudioIn != 0 {
		t.Fatalf("line out channels %d in / %d out", st.Slots[10].AudioIn, st.Slots[10].AudioOut)
	}
	if st.Slots[0].AudioIn != 4 { // microphone card: 4 channels
		t.Fatalf("mic channels %d", st.Slots[0].AudioIn)
	}
}
