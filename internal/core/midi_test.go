// SPDX-License-Identifier: AGPL-3.0-or-later
package core

import (
	"bufio"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeShell plays the role of the CoreMIDI bridge in the macOS window shell.
type fakeShell struct {
	toCore io.Writer
	lines  chan string
}

func attachFakeShell(t *testing.T, a *App) *fakeShell {
	inR, inW := io.Pipe()   // shell -> core
	outR, outW := io.Pipe() // core -> shell
	fs := &fakeShell{toCore: inW, lines: make(chan string, 4096)}
	go func() {
		sc := bufio.NewScanner(outR)
		for sc.Scan() {
			fs.lines <- sc.Text()
		}
	}()
	a.AttachMIDI(inR, outW)
	fs.send("S MIDI ready")
	fs.send("D")
	fs.send("I 111 Fader Box")
	fs.send("O 222 Fader Box")
	fs.send("I 333 Keyboard")
	fs.send("E")
	return fs
}

func (f *fakeShell) send(l string) { io.WriteString(f.toCore, l+"\n") }

// expect waits for an outgoing line with the given prefix.
func (f *fakeShell) expect(t *testing.T, want string) {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		select {
		case l := <-f.lines:
			if strings.EqualFold(l, want) {
				return
			}
		case <-deadline:
			t.Fatalf("no %q from core", want)
		}
	}
}

func (f *fakeShell) drain() {
	for {
		select {
		case <-f.lines:
		case <-time.After(150 * time.Millisecond):
			return
		}
	}
}

func (f *fakeShell) none(t *testing.T, prefix string) {
	t.Helper()
	end := time.After(500 * time.Millisecond)
	for {
		select {
		case l := <-f.lines:
			if strings.HasPrefix(strings.ToLower(l), strings.ToLower(prefix)) {
				t.Fatalf("unexpected %q", l)
			}
		case <-end:
			return
		}
	}
}

func param(a *App, id string) Param {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, p := range a.paramsLocked() {
		if p.ID == id {
			return p
		}
	}
	return Param{}
}

func TestMIDITranslatorAndLearn(t *testing.T) {
	a, sim := newTestApp(t)
	a.Poll()
	fs := attachFakeShell(t, a)
	waitFor(t, func() bool { return len(a.State().MIDI.Inputs) == 2 })

	// initial dump: current gain of MIC 1/1 (+25 dB -> 36) goes out on the virtual port
	fs.expect(t, "T V b00124")
	fs.drain()

	// DAW sets MIC 1/2 gain: channel 1 (slot 1), CC 2, value 40 -> -11+40 = +29 dB
	fs.send("M V b00228")
	waitFor(t, func() bool { return param(a, "s0c2.gain").Value == 29 })
	waitFor(t, func() bool {
		var got []byte
		sim.Mutate(func(s *SimPort) { got = append([]byte(nil), s.Slots[0].Command...) })
		return micDecode(&Card{CommandSize: 8, Command: got}, 2, 0).Gain == 29
	})
	fs.none(t, "T V b00228") // no echo of the DAW's own value

	// a change made in the app is sent to the DAW: 48 V on DL5 channel 3 (slot 3 -> MIDI ch 3, CC 23)
	a.Do(Op{Op: "dl5", Strip: "s2c3", Field: "phantom", Value: true})
	fs.expect(t, "T V b2177f")

	// a JS plug-in asks for everything of slot 1 (CC 127): the values are sent again
	fs.drain()
	fs.send("M V b07f7f")
	fs.expect(t, "T V b00124")
	fs.expect(t, "T V b00228")

	// learn a controller fader for MIC 1/3 gain
	a.Do(Op{Op: "midiLearn", Text: "s0c3.gain"})
	fs.send("M 111 b0107f") // CC 16 from the Fader Box
	waitFor(t, func() bool { return len(a.State().MIDI.Mappings) == 1 })
	m := a.State().MIDI.Mappings[0]
	if m.Device != "Fader Box" || m.Msg != "Ch 1 CC 16" || m.Mode != "abs" {
		t.Fatalf("mapping %+v", m)
	}
	fs.send("M 111 b01000")
	waitFor(t, func() bool { return param(a, "s0c3.gain").Value == MicGainMin })
	fs.send("M 111 b0107f")
	waitFor(t, func() bool { return param(a, "s0c3.gain").Value == MicGainMax })
	// the DAW still hears about it, the controller itself is not echoed
	fs.expect(t, "T V b00356")
	fs.none(t, "T 222 b010")

	// a change from the app moves the motor fader on the controller's output
	// (right after the controller was touched it is delayed past the echo window)
	time.Sleep(450 * time.Millisecond)
	a.Do(Op{Op: "mic", Strip: "s0c3", Field: "gain", Value: float64(32)})
	fs.expect(t, fmt.Sprintf("T 222 b010%02x", ((32-MicGainMin)*127+43)/86))

	// learn a pad as a toggle for 48 V of MIC 1/4
	a.Do(Op{Op: "midiLearn", Text: "s0c4.phantom"})
	fs.send("M 111 90247f")
	waitFor(t, func() bool { return len(a.State().MIDI.Mappings) == 2 })
	before := param(a, "s0c4.phantom").Value
	fs.send("M 111 90247f") // press
	fs.send("M 111 802400") // release does nothing in toggle mode
	waitFor(t, func() bool { return param(a, "s0c4.phantom").Value == 1-before })

	// relative encoder
	a.Do(Op{Op: "midiMode", Index: 0, Text: "rel"})
	g := param(a, "s0c3.gain").Value
	fs.send("M 111 b01003") // +3
	waitFor(t, func() bool { return param(a, "s0c3.gain").Value == g+3 })
	fs.send("M 111 b0107e") // -2
	waitFor(t, func() bool { return param(a, "s0c3.gain").Value == g+1 })

	// a different device sending the same CC is ignored
	fs.send("M 333 b01000")
	time.Sleep(100 * time.Millisecond)
	if param(a, "s0c3.gain").Value != g+1 {
		t.Fatal("other device must not drive the mapping")
	}

	// mappings survive a restart
	b := NewApp(LoadConfig(filepath.Join(filepath.Dir(a.cfg.Path), "settings.xml")))
	if len(b.midi.conf.Mappings) != 2 || b.midi.conf.Mappings[0].Mode != "rel" {
		t.Fatalf("not persisted: %+v", b.midi.conf.Mappings)
	}
}

func TestMIDIToggleAndEnums(t *testing.T) {
	a, _ := newTestApp(t)
	a.Poll()
	fs := attachFakeShell(t, a)
	fs.drain()
	// GPO 5 of the GPIO card in slot 9 via the virtual port: channel 9, CC 5
	fs.send("M V b8057f")
	waitFor(t, func() bool { return param(a, "s8c1.gpo5").Value == 1 })
	// AES FS out group 2 to 96 kHz: slot 4 -> channel 4, CC 12, value 2
	fs.send("M V b30c02")
	waitFor(t, func() bool { return param(a, "s3c1.fs2").Value == 2 })
	// GPI are read-only
	fs.send("M V b81500")
	time.Sleep(100 * time.Millisecond)
	if !param(a, "s8c1.gpi1").ReadOnly {
		t.Fatal("GPI must be read-only")
	}
	// a fader can't be learned from a key
	a.Do(Op{Op: "midiLearn", Text: "s0c1.gain"})
	fs.send("M 333 903c40")
	time.Sleep(100 * time.Millisecond)
	if len(a.State().MIDI.Mappings) != 0 || !strings.Contains(a.State().Status, "fader") {
		t.Fatalf("status %q", a.State().Status)
	}
}
