// SPDX-License-Identifier: AGPL-3.0-or-later
package core

import (
	"bufio"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"remote21/internal/midi"
	"remote21/internal/proto"
)

// ---------------------------------------------------------------------------
// Rack parameters as seen by MIDI
// ---------------------------------------------------------------------------

// Param is one controllable (or, if ReadOnly, observable) value of the rack.
type Param struct {
	ID       string `json:"id"`
	Label    string `json:"label"`
	Kind     string `json:"kind"` // "range", "bool", "enum"
	Min      int    `json:"min"`
	Max      int    `json:"max"`
	Value    int    `json:"value"`
	ReadOnly bool   `json:"readOnly"`
	slot     int
	field    string
	ch       int
	kindOf   string // strip kind
}

var fieldNames = map[string]string{
	"gain": "Gain", "phantom": "48 V", "hpf": "HPF", "insert": "Insert", "softclip": "Soft clip",
	"dcs": "DC servo", "cms": "CM servo",
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

// paramsLocked lists every parameter of the cards currently in the rack.
func (a *App) paramsLocked() []Param {
	var out []Param
	for _, s := range a.strips {
		c := s.Card
		name := s.FixedLabel()
		if l, _ := s.param("Label"); l != "" {
			name += " (" + l + ")"
		}
		add := func(field, kind string, min, max, v int, ro bool) {
			lbl := fieldNames[field]
			if lbl == "" {
				lbl = field
			}
			out = append(out, Param{ID: s.ID() + "." + field, Label: name + " " + lbl, Kind: kind, Min: min, Max: max,
				Value: v, ReadOnly: ro || s.LinkedBy != nil, slot: c.Slot, field: field, ch: s.Ch, kindOf: s.Kind})
		}
		switch s.Kind {
		case KindMic:
			m := micDecode(c, s.Ch, s.lastGain)
			add("gain", "range", MicGainMin, MicGainMax, m.Gain, false)
			add("phantom", "bool", 0, 1, b2i(m.Phantom), false)
			add("hpf", "bool", 0, 1, b2i(m.HPF), false)
			add("insert", "bool", 0, 1, b2i(m.Insert), false)
			add("softclip", "bool", 0, 1, b2i(m.Softclip), false)
		case KindDL5:
			m := dl5Decode(c, s.Ch)
			add("gain", "range", DL5GainMin, DL5GainMax, m.Gain, false)
			add("phantom", "bool", 0, 1, b2i(m.Phantom), false)
			add("hpf", "bool", 0, 1, b2i(m.HPF), false)
			add("dcs", "bool", 0, 1, b2i(m.DCS), false)
			add("cms", "bool", 0, 1, b2i(m.CMS), false)
		case KindGPIO:
			g := gpioDecode(c)
			for i := 0; i < 16; i++ {
				add("gpo"+strconv.Itoa(i+1), "bool", 0, 1, b2i(g.GPO[i]), false)
			}
			for i := 0; i < 16; i++ {
				add("gpi"+strconv.Itoa(i+1), "bool", 0, 1, b2i(g.GPI[i]), true)
			}
		case KindAES:
			st := aesDecode(c)
			if st.HasSFCIn {
				for i := 0; i < 8; i++ {
					add("sfcIn"+strconv.Itoa(i+1), "bool", 0, 1, b2i(st.SFCIn[i]), false)
				}
			}
			if st.HasOut {
				for g := 0; g < 2; g++ {
					add("sfcOut"+strconv.Itoa(g+1), "bool", 0, 1, b2i(st.SFCOut[g]), false)
				}
				for g := 0; g < 2; g++ {
					add("fs"+strconv.Itoa(g+1), "enum", 0, 3, st.FsOut[g], false)
				}
				for i := 0; i < 8; i++ {
					add("wl"+strconv.Itoa(i+1), "enum", 0, 3, st.Wordlength[i], false)
				}
			}
		}
	}
	return out
}

func numSuffix(field, prefix string) (int, bool) {
	if !strings.HasPrefix(field, prefix) {
		return 0, false
	}
	n, err := strconv.Atoi(field[len(prefix):])
	return n, err == nil
}

// setParamLocked applies a value through the same code paths as the UI.
func (a *App) setParamLocked(id string, v int) error {
	dot := strings.IndexByte(id, '.')
	if dot < 0 {
		return fmt.Errorf("bad parameter %q", id)
	}
	s := a.stripByID(id[:dot])
	if s == nil {
		return fmt.Errorf("no such channel %s", id[:dot])
	}
	if s.LinkedBy != nil {
		return nil // slave of a stereo link follows its master
	}
	f := id[dot+1:]
	c := s.Card
	switch s.Kind {
	case KindMic:
		if f == "gain" {
			return a.setMicField(s, f, v)
		}
		return a.setMicField(s, f, v != 0)
	case KindDL5:
		if f == "gain" {
			return a.setDL5Field(s, f, v)
		}
		return a.setDL5Field(s, f, v != 0)
	case KindGPIO:
		if n, ok := numSuffix(f, "gpo"); ok && n >= 1 && n <= 16 {
			c.setBit((n-1)/8, (n-1)%8, v != 0)
			a.queueCard(c)
			return nil
		}
	case KindAES:
		if n, ok := numSuffix(f, "sfcIn"); ok && n >= 1 && n <= 8 {
			aesSetSFCIn(c, n-1, v != 0)
		} else if n, ok := numSuffix(f, "sfcOut"); ok && n >= 1 && n <= 2 {
			aesSetSFCOut(c, n-1, v != 0)
		} else if n, ok := numSuffix(f, "fs"); ok && n >= 1 && n <= 2 {
			aesSetFsOut(c, n-1, clamp(v, 0, 3))
		} else if n, ok := numSuffix(f, "wl"); ok && n >= 1 && n <= 8 {
			aesSetWordlength(c, n-1, clamp(v, 0, 3))
		} else {
			break
		}
		a.queueCard(c)
		return nil
	}
	return fmt.Errorf("parameter %s cannot be set", id)
}

// ---------------------------------------------------------------------------
// Fixed translator map on the virtual "21 Remote" port (for DAWs)
// MIDI channel = slot number (1-12). Values are exact: gain CC value = dB - minimum.
// ---------------------------------------------------------------------------

func translatorCC(p Param) (ch, cc int, ok bool) {
	ch = p.slot
	switch p.kindOf {
	case KindMic, KindDL5:
		base := map[string]int{"gain": 0, "phantom": 20, "hpf": 30, "insert": 40, "dcs": 40, "softclip": 50, "cms": 50}
		b, found := base[p.field]
		return ch, b + p.ch, found
	case KindGPIO:
		if n, ok := numSuffix(p.field, "gpo"); ok {
			return ch, n, true
		}
		if n, ok := numSuffix(p.field, "gpi"); ok {
			return ch, 20 + n, true
		}
	case KindAES:
		if n, ok := numSuffix(p.field, "sfcIn"); ok {
			return ch, n, true
		}
		if n, ok := numSuffix(p.field, "sfcOut"); ok {
			return ch, 8 + n, true
		}
		if n, ok := numSuffix(p.field, "fs"); ok {
			return ch, 10 + n, true
		}
		if n, ok := numSuffix(p.field, "wl"); ok {
			return ch, 12 + n, true
		}
	}
	return 0, 0, false
}

func translatorValue(p Param) int {
	switch p.Kind {
	case "bool":
		return 127 * p.Value
	case "range":
		return clamp(p.Value-p.Min, 0, 127)
	}
	return clamp(p.Value, 0, 127)
}

func translatorParse(p Param, v int) int {
	switch p.Kind {
	case "bool":
		return b2i(v >= 64)
	case "range":
		return clamp(p.Min+v, p.Min, p.Max)
	}
	return clamp(v, p.Min, p.Max)
}

// TranslatorRow documents the fixed map (shown in Settings and README).
type TranslatorRow struct {
	Card  string `json:"card"`
	CC    string `json:"cc"`
	What  string `json:"what"`
	Value string `json:"value"`
}

// DumpCC on a slot's MIDI channel makes the app send every value of that slot.
const DumpCC = 127

var TranslatorDoc = []TranslatorRow{
	{"Mic", "1–4", "Gain, channel 1–4", "0–86 = −11…+75 dB"},
	{"DL5 Mic", "1–8", "Gain, channel 1–8", "0–75 = −13…+62 dB"},
	{"Mic, DL5", "21–28", "48 V", "0 off, 127 on (≥64 = on)"},
	{"Mic, DL5", "31–38", "HPF", "0 / 127"},
	{"Mic / DL5", "41–48", "Insert / DC servo", "0 / 127"},
	{"Mic / DL5", "51–58", "Soft clip / CM servo", "0 / 127"},
	{"GPIO", "1–16", "GPO 1–16", "0 / 127"},
	{"GPIO", "21–36", "GPI 1–16 (sent only)", "0 / 127"},
	{"AES/EBU", "1–8", "SFC input 1–8", "0 / 127"},
	{"AES/EBU", "9–10", "SFC output 1–4, 5–8", "0 / 127"},
	{"AES/EBU", "11–12", "FS out 1–4, 5–8", "0 = 44.1, 1 = 48, 2 = 96, 3 = ext"},
	{"AES/EBU", "13–20", "Word length out 1–8", "0 = 16, 1 = 18, 2 = 20, 3 = 24 bit"},
	{"Any", "127", "Request: send all values of this slot", "any value"},
}

// ---------------------------------------------------------------------------
// Learned mappings (controllers)
// ---------------------------------------------------------------------------

type Mapping struct {
	Param     string `json:"param"`
	Kind      int    `json:"kind"` // midi.CC, midi.NoteOn, midi.PitchBend
	Ch        int    `json:"ch"`
	Num       int    `json:"num"`
	Device    string `json:"device"`    // device name
	DeviceUID string `json:"deviceUid"` // CoreMIDI unique ID, "V" for the virtual port
	Mode      string `json:"mode"`      // abs, rel, toggle, direct, cycle
}

func (m Mapping) msgText() string {
	switch m.Kind {
	case midi.CC:
		return fmt.Sprintf("Ch %d CC %d", m.Ch+1, m.Num)
	case midi.NoteOn:
		return fmt.Sprintf("Ch %d Note %d", m.Ch+1, m.Num)
	case midi.PitchBend:
		return fmt.Sprintf("Ch %d Pitch bend", m.Ch+1)
	}
	return "?"
}

func modesFor(kind string, msgKind int) []string {
	switch kind {
	case "range":
		if msgKind == midi.CC {
			return []string{"abs", "rel"}
		}
		return []string{"abs"}
	case "bool":
		return []string{"toggle", "direct"}
	}
	if msgKind == midi.NoteOn {
		return []string{"cycle"}
	}
	return []string{"abs"}
}

type MIDIDevice struct {
	UID  string `json:"uid"`
	Name string `json:"name"`
}

type midiConf struct {
	Virtual  bool      `json:"virtual"`
	Feedback bool      `json:"feedback"`
	Mappings []Mapping `json:"mappings"`
}

type midiState struct {
	mu        sync.Mutex // guards out (writes to the shell)
	out       io.Writer
	available bool
	status    string
	conf      midiConf
	path      string
	inputs    []MIDIDevice
	outputs   []MIDIDevice
	parsers   map[string]*midi.Parser
	learn     string
	learnEnd  time.Time
	sent      map[string]int       // last value sent per destination+message
	echo      map[string]time.Time // param|device -> last time it came from there
	monitor   []LogEntry
	pendingIn []LogEntry
}

func (a *App) midiInit(dir string) {
	ms := &midiState{conf: midiConf{Virtual: true, Feedback: true}, path: filepath.Join(dir, "midi.json"),
		parsers: map[string]*midi.Parser{}, sent: map[string]int{}, echo: map[string]time.Time{}, status: "MIDI needs the macOS app (not available in a browser)."}
	if b, err := os.ReadFile(ms.path); err == nil {
		json.Unmarshal(b, &ms.conf)
	}
	a.midi = ms
}

func (a *App) midiSaveLocked() {
	b, _ := json.MarshalIndent(a.midi.conf, "", "  ")
	os.MkdirAll(filepath.Dir(a.midi.path), 0o755)
	if err := os.WriteFile(a.midi.path, b, 0o644); err != nil {
		log.Printf("save midi: %v", err)
	}
}

func (a *App) midiLog(dir, text string) {
	ms := a.midi
	ms.monitor = append(ms.monitor, LogEntry{T: time.Now().Format("15:04:05.000"), Dir: dir, Text: text})
	if len(ms.monitor) > 300 {
		ms.monitor = ms.monitor[len(ms.monitor)-300:]
	}
}

func (a *App) MIDILog() []LogEntry {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]LogEntry(nil), a.midi.monitor...)
}

// AttachMIDI connects the CoreMIDI bridge of the window shell: r carries
// device lists and incoming messages, w takes outgoing messages.
func (a *App) AttachMIDI(r io.Reader, w io.Writer) {
	a.mu.Lock()
	a.midi.out = w
	a.midi.available = true
	a.midi.status = "Starting MIDI…"
	a.mu.Unlock()
	// subscribe before anything can change, so the first full update is never missed
	ch, _ := a.Subscribe()
	go a.midiFeedbackLoop(ch)
	go a.midiReadLoop(r)
	a.mu.Lock()
	a.changed()
	a.mu.Unlock()
}

func (a *App) bridgeSend(line string) {
	ms := a.midi
	ms.mu.Lock()
	defer ms.mu.Unlock()
	if ms.out != nil {
		io.WriteString(ms.out, line+"\n")
	}
}

func (a *App) midiReadLoop(r io.Reader) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 512*1024) // long SysEx lines
	var ins, outs []MIDIDevice
	for sc.Scan() {
		line := sc.Text()
		if line == "" {
			continue
		}
		cmd, rest, _ := strings.Cut(line, " ")
		switch cmd {
		case "S":
			a.mu.Lock()
			a.midi.status = rest
			a.changed()
			a.mu.Unlock()
		case "D":
			ins, outs = nil, nil
		case "I", "O":
			uid, name, _ := strings.Cut(rest, " ")
			d := MIDIDevice{UID: uid, Name: name}
			if cmd == "I" {
				ins = append(ins, d)
			} else {
				outs = append(outs, d)
			}
		case "E":
			a.mu.Lock()
			a.midi.inputs, a.midi.outputs = ins, outs
			a.midi.sent = map[string]int{} // new devices get a full update
			a.changed()
			a.mu.Unlock()
		case "M":
			src, hx, _ := strings.Cut(rest, " ")
			b, err := hex.DecodeString(hx)
			if err == nil {
				a.MIDIIn(src, b)
			}
		}
	}
	a.mu.Lock()
	a.midi.available = false
	a.midi.status = "MIDI bridge closed."
	a.changed()
	a.mu.Unlock()
}

func (a *App) deviceName(uid string) string {
	if uid == "V" {
		return "21 Remote (virtual port)"
	}
	for _, d := range a.midi.inputs {
		if d.UID == uid {
			return d.Name
		}
	}
	return "device " + uid
}

func (a *App) mappingMatches(m Mapping, src string) bool {
	if m.DeviceUID == src {
		return true
	}
	if src == "V" || m.DeviceUID == "V" {
		return false
	}
	// same model plugged into another Mac/port: match by name if the first one is absent
	if a.deviceName(src) != m.Device {
		return false
	}
	for _, d := range a.midi.inputs {
		if d.UID == m.DeviceUID {
			return false
		}
	}
	return true
}

func msgKey(kind int) int {
	if kind == midi.NoteOff {
		return midi.NoteOn
	}
	return kind
}

// MIDIIn handles raw bytes from one source (uid, or "V" for the virtual port).
func (a *App) MIDIIn(src string, b []byte) {
	a.mu.Lock()
	defer a.mu.Unlock()
	ms := a.midi
	p := ms.parsers[src]
	if p == nil {
		p = &midi.Parser{}
		ms.parsers[src] = p
	}
	msgs := p.Feed(b)
	if len(msgs) == 0 {
		return
	}
	dev := a.deviceName(src)
	params := a.paramsLocked()
	byID := map[string]Param{}
	for _, x := range params {
		byID[x.ID] = x
	}
	changed := false
	for _, m := range msgs {
		a.midiLog("in", dev+": "+m.String())
		if m.Kind == midi.SysEx {
			continue
		}
		// learning
		if ms.learn != "" && time.Now().Before(ms.learnEnd) && (m.Kind == midi.CC || m.Kind == midi.NoteOn || m.Kind == midi.PitchBend) {
			if ms.conf.Virtual || src != "V" {
				a.learnFrom(src, dev, m, byID[ms.learn])
				changed = true
				continue
			}
		}
		// fixed translator on the virtual port. CC 127 asks for all values of
		// that slot (used by the JS plug-ins when they open or change channel).
		if src == "V" && ms.conf.Virtual && m.Kind == midi.CC && m.Num == DumpCC {
			prefix := fmt.Sprintf("V|%d|%d|", midi.CC, m.Ch)
			for k := range ms.sent {
				if strings.HasPrefix(k, prefix) {
					delete(ms.sent, k)
				}
			}
			changed = true
			continue
		}
		if src == "V" && ms.conf.Virtual && m.Kind == midi.CC {
			for _, x := range params {
				if ch, cc, ok := translatorCC(x); ok && ch == m.Ch && cc == m.Num && !x.ReadOnly {
					nv := translatorParse(x, m.Val)
					ms.echo[x.ID+"|"+dev] = time.Now()
					if nv != x.Value {
						if err := a.setParamLocked(x.ID, nv); err == nil {
							changed = true
						}
					}
				}
			}
		}
		// learned controls
		for _, mp := range ms.conf.Mappings {
			if mp.Kind != msgKey(m.Kind) || mp.Ch != m.Ch || (mp.Kind != midi.PitchBend && mp.Num != m.Num) || !a.mappingMatches(mp, src) {
				continue
			}
			x, ok := byID[mp.Param]
			if !ok || x.ReadOnly {
				continue
			}
			nv, apply := mappedValue(mp, m, x)
			ms.echo[x.ID+"|"+dev] = time.Now()
			if apply && nv != x.Value {
				if err := a.setParamLocked(x.ID, nv); err == nil {
					x.Value = nv
					byID[x.ID] = x
					changed = true
				}
			}
		}
	}
	if changed {
		a.changed()
	}
}

func mappedValue(mp Mapping, m midi.Msg, p Param) (int, bool) {
	switch p.Kind {
	case "range":
		switch {
		case mp.Mode == "rel" && m.Kind == midi.CC:
			d := m.Val
			if d >= 64 {
				d -= 128
			}
			return clamp(p.Value+d, p.Min, p.Max), d != 0
		case m.Kind == midi.PitchBend:
			return p.Min + (m.Val*(p.Max-p.Min)+8191)/16383, true
		case m.Kind == midi.CC:
			return p.Min + (m.Val*(p.Max-p.Min)+63)/127, true
		}
	case "bool":
		on := (m.Kind == midi.NoteOn) || (m.Kind == midi.CC && m.Val > 0)
		if mp.Mode == "direct" {
			if m.Kind == midi.CC {
				return b2i(m.Val >= 64), true
			}
			return b2i(m.Kind == midi.NoteOn), true
		}
		if on {
			return 1 - p.Value, true
		}
	case "enum":
		if m.Kind == midi.NoteOn {
			return (p.Value + 1) % (p.Max + 1), true
		}
		if m.Kind == midi.CC {
			return clamp((m.Val*(p.Max+1))/128, 0, p.Max), true
		}
	}
	return 0, false
}

func (a *App) learnFrom(src, dev string, m midi.Msg, p Param) {
	ms := a.midi
	ms.learn = ""
	if p.ID == "" {
		return
	}
	if p.Kind == "range" && m.Kind == midi.NoteOn {
		a.setStatus("error", "A fader needs a knob, fader or pitch bend, not a key or pad. Try MIDI Learn again.")
		return
	}
	mp := Mapping{Param: p.ID, Kind: m.Kind, Ch: m.Ch, Num: m.Num, Device: dev, DeviceUID: src}
	if m.Kind == midi.PitchBend {
		mp.Num = 0
	}
	mp.Mode = modesFor(p.Kind, m.Kind)[0]
	// one message drives one parameter
	out := ms.conf.Mappings[:0]
	for _, x := range ms.conf.Mappings {
		if !(x.DeviceUID == mp.DeviceUID && x.Kind == mp.Kind && x.Ch == mp.Ch && x.Num == mp.Num) {
			out = append(out, x)
		}
	}
	ms.conf.Mappings = append(out, mp)
	a.midiSaveLocked()
	ms.sent = map[string]int{}
	a.setStatus("info", fmt.Sprintf("MIDI Learn: %s → %s", mp.msgText()+" ("+dev+")", p.Label))
}

// ---------------------------------------------------------------------------
// Feedback: rack/app changes -> virtual port and controller outputs
// ---------------------------------------------------------------------------

func (a *App) midiFeedbackLoop(ch chan struct{}) {
	for range ch {
		time.Sleep(15 * time.Millisecond)
		a.mu.Lock()
		lines := a.midiFeedbackLocked()
		a.mu.Unlock()
		for _, l := range lines {
			a.bridgeSend(l)
		}
	}
}

func (a *App) outputsNamed(name string) []string {
	var out []string
	for _, d := range a.midi.outputs {
		if d.Name == name {
			out = append(out, d.UID)
		}
	}
	return out
}

func (a *App) midiFeedbackLocked() []string {
	ms := a.midi
	if !ms.available {
		return nil
	}
	var lines []string
	now := time.Now()
	recent := func(k string) bool { t, ok := ms.echo[k]; return ok && now.Sub(t) < 400*time.Millisecond }
	// exact: the value round-trips unchanged (translator), so a suppressed echo
	// can be recorded as sent. Scaled controller values are re-sent once the
	// echo window is over so motor faders end up where the rack really is.
	emit := func(dest, dev string, p Param, m midi.Msg, exact bool) {
		key := fmt.Sprintf("%s|%d|%d|%d", dest, m.Kind, m.Ch, m.Num)
		if old, ok := ms.sent[key]; ok && old == m.Val {
			return
		}
		if recent(p.ID + "|" + dev) {
			if exact {
				ms.sent[key] = m.Val
			}
			return
		}
		ms.sent[key] = m.Val
		lines = append(lines, "T "+dest+" "+hex.EncodeToString(m.Bytes()))
		a.midiLog("out", dev+": "+m.String())
	}
	params := a.paramsLocked()
	byID := map[string]Param{}
	for _, p := range params {
		byID[p.ID] = p
	}
	if ms.conf.Virtual {
		for _, p := range params {
			if ch, cc, ok := translatorCC(p); ok {
				emit("V", a.deviceName("V"), p, midi.Msg{Kind: midi.CC, Ch: ch, Num: cc, Val: translatorValue(p)}, true)
			}
		}
	}
	if ms.conf.Feedback {
		for _, mp := range ms.conf.Mappings {
			p, ok := byID[mp.Param]
			if !ok || mp.Mode == "rel" {
				continue
			}
			var m midi.Msg
			switch mp.Kind {
			case midi.CC:
				v := 127 * p.Value
				if p.Kind != "bool" && p.Max > p.Min {
					v = ((p.Value-p.Min)*127 + (p.Max-p.Min)/2) / (p.Max - p.Min)
				}
				m = midi.Msg{Kind: midi.CC, Ch: mp.Ch, Num: mp.Num, Val: clamp(v, 0, 127)}
			case midi.NoteOn:
				m = midi.Msg{Kind: midi.NoteOn, Ch: mp.Ch, Num: mp.Num, Val: 127 * b2i(p.Value != 0)}
			case midi.PitchBend:
				v := 0
				if p.Max > p.Min {
					v = (p.Value - p.Min) * 16383 / (p.Max - p.Min)
				}
				m = midi.Msg{Kind: midi.PitchBend, Ch: mp.Ch, Val: v}
			default:
				continue
			}
			if mp.DeviceUID == "V" {
				if !ms.conf.Virtual {
					continue
				}
				emit("V", mp.Device, p, m, false)
				continue
			}
			for _, uid := range a.outputsNamed(mp.Device) {
				emit(uid, mp.Device, p, m, false)
			}
		}
	}
	return lines
}

// ---------------------------------------------------------------------------
// UI state and operations
// ---------------------------------------------------------------------------

type MappingView struct {
	Index  int      `json:"index"`
	Param  string   `json:"param"`
	Label  string   `json:"label"`
	Msg    string   `json:"msg"`
	Device string   `json:"device"`
	Mode   string   `json:"mode"`
	Modes  []string `json:"modes"`
	Absent bool     `json:"absent"`
}

type MIDIView struct {
	Available  bool            `json:"available"`
	Status     string          `json:"status"`
	Virtual    bool            `json:"virtual"`
	Feedback   bool            `json:"feedback"`
	Inputs     []MIDIDevice    `json:"inputs"`
	Outputs    []MIDIDevice    `json:"outputs"`
	Mappings   []MappingView   `json:"mappings"`
	Learning   string          `json:"learning"`
	Mapped     []string        `json:"mapped"`
	Translator []TranslatorRow `json:"translator"`
}

func (a *App) midiViewLocked() MIDIView {
	ms := a.midi
	v := MIDIView{Available: ms.available, Status: ms.status, Virtual: ms.conf.Virtual, Feedback: ms.conf.Feedback,
		Inputs: ms.inputs, Outputs: ms.outputs, Translator: TranslatorDoc}
	if ms.learn != "" && time.Now().Before(ms.learnEnd) {
		v.Learning = ms.learn
	}
	labels, kinds := map[string]string{}, map[string]string{}
	for _, p := range a.paramsLocked() {
		labels[p.ID], kinds[p.ID] = p.Label, p.Kind
	}
	seen := map[string]bool{}
	for i, mp := range ms.conf.Mappings {
		lbl, ok := labels[mp.Param]
		if !ok {
			lbl = mp.Param + " (card not present)"
		}
		kind := kinds[mp.Param]
		if kind == "" {
			kind = "range"
		}
		v.Mappings = append(v.Mappings, MappingView{Index: i, Param: mp.Param, Label: lbl, Msg: mp.msgText(), Device: mp.Device,
			Mode: mp.Mode, Modes: modesFor(kind, mp.Kind), Absent: !ok})
		if !seen[mp.Param] {
			v.Mapped = append(v.Mapped, mp.Param)
			seen[mp.Param] = true
		}
	}
	sort.Strings(v.Mapped)
	return v
}

func (a *App) midiOp(op Op) (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	defer a.changed()
	ms := a.midi
	switch op.Op {
	case "midiVirtual":
		ms.conf.Virtual = asBool(op.Value)
		ms.sent = map[string]int{}
	case "midiFeedback":
		ms.conf.Feedback = asBool(op.Value)
		ms.sent = map[string]int{}
	case "midiLearn":
		if !ms.available {
			return "MIDI works in the macOS app only.", nil
		}
		ms.learn = op.Text
		ms.learnEnd = time.Now().Add(20 * time.Second)
		return "", nil
	case "midiLearnCancel":
		ms.learn = ""
		return "", nil
	case "midiMode":
		if op.Index < 0 || op.Index >= len(ms.conf.Mappings) {
			return "", fmt.Errorf("no such mapping")
		}
		ms.conf.Mappings[op.Index].Mode = op.Text
		ms.sent = map[string]int{}
	case "midiDelete":
		if op.Index < 0 || op.Index >= len(ms.conf.Mappings) {
			return "", fmt.Errorf("no such mapping")
		}
		ms.conf.Mappings = append(ms.conf.Mappings[:op.Index], ms.conf.Mappings[op.Index+1:]...)
	case "midiDeleteParam":
		out := ms.conf.Mappings[:0]
		for _, m := range ms.conf.Mappings {
			if m.Param != op.Text {
				out = append(out, m)
			}
		}
		ms.conf.Mappings = out
	case "midiClear":
		ms.conf.Mappings = nil
	case "midiDump":
		ms.sent = map[string]int{}
		return "", nil
	case "midiRescan":
		go a.bridgeSend("L")
		return "", nil
	default:
		return "", fmt.Errorf("unknown op %q", op.Op)
	}
	a.midiSaveLocked()
	return "", nil
}

// ---------------------------------------------------------------------------
// Access for DAW plug-ins (see internal/pluginapi)
// ---------------------------------------------------------------------------

// PluginChannel is one channel strip as offered to plug-ins.
type PluginChannel struct {
	ID      string `json:"id"`
	Slot    int    `json:"slot"`
	Ch      int    `json:"ch"`
	Kind    string `json:"kind"`
	Name    string `json:"name"`  // fixed label, e.g. "MIC 1/2"
	Label   string `json:"label"` // user label
	Comment string `json:"comment"`
	Color   string `json:"color"`
	IDs     string `json:"ids"`
	Linked  bool   `json:"linked"` // slave of a stereo link (read-only)
}

type PluginLayout struct {
	Rack     string          `json:"rack"`
	Online   bool            `json:"online"`
	Module   string          `json:"module"`
	Channels []PluginChannel `json:"channels"`
	Params   []Param         `json:"params"`
}

func (a *App) PluginLayout() PluginLayout {
	a.mu.Lock()
	defer a.mu.Unlock()
	l := PluginLayout{Rack: a.rack.Label, Online: a.link != nil && a.rack.Online && a.rack.Module != 0xFFFF && a.rack.Module != 0}
	if l.Online {
		l.Module = proto.ModuleDescription(a.rack.Module)
	}
	for _, s := range a.strips {
		label, _ := s.param("Label")
		comment, _ := s.param("Comment")
		l.Channels = append(l.Channels, PluginChannel{ID: s.ID(), Slot: s.Card.Slot, Ch: s.Ch, Kind: s.Kind, Name: s.FixedLabel(),
			Label: label, Comment: comment, Color: s.Color(), IDs: s.ChannelIDs(), Linked: s.LinkedBy != nil})
	}
	l.Params = a.paramsLocked()
	return l
}

func (a *App) ParamValues() map[string]int {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := map[string]int{}
	for _, p := range a.paramsLocked() {
		out[p.ID] = p.Value
	}
	return out
}

func (a *App) SetParam(id string, v int) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	err := a.setParamLocked(id, v)
	if err == nil {
		a.changed()
	}
	return err
}
