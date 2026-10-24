// SPDX-License-Identifier: AGPL-3.0-or-later
package core

import (
	"fmt"
	"log"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// ---- undo / redo ----
//
// Every user change to the rack (controls, names, colours, comments, order,
// visibility, snapshots) is undoable. Connecting and settings are not. A step
// is a copy of the editable state taken before the change.

const undoDepth = 200

type undoCard struct {
	typ    int
	show   bool
	cmd    []byte
	params *Params
}

type undoState struct {
	desc       string
	label      string
	cards      [12]undoCard
	order      []string
	gains      map[string]int
	phantomOff []string
	phantomOn  bool
}

func (p *Params) clone() *Params {
	n := NewParams()
	p.Each(func(k, v string) { n.Set(k, v) })
	return n
}

func (a *App) captureLocked(desc string) *undoState {
	u := &undoState{desc: desc, label: a.rack.Label, gains: map[string]int{},
		phantomOff: append([]string(nil), a.phantomOff...), phantomOn: a.phantomKilled}
	for i, c := range a.rack.Cards {
		u.cards[i] = undoCard{typ: c.Type, show: c.Show, cmd: append([]byte(nil), c.Command...), params: c.Params.clone()}
	}
	for _, s := range a.strips {
		u.order = append(u.order, s.ID())
		u.gains[s.ID()] = s.lastGain
	}
	return u
}

// restoreLocked puts a captured state back. Cards whose type changed since are left alone.
func (a *App) restoreLocked(u *undoState) {
	a.rack.Label = u.label
	for i, c := range a.rack.Cards {
		uc := u.cards[i]
		if uc.typ != c.Type {
			continue
		}
		c.Show = uc.show
		c.Params = uc.params.clone()
		if string(c.Command) != string(uc.cmd) && len(uc.cmd) == len(c.Command) {
			copy(c.Command, uc.cmd)
			a.queueCard(c)
		}
	}
	pos := map[string]int{}
	for i, id := range u.order {
		pos[id] = i
	}
	sort.SliceStable(a.strips, func(i, j int) bool {
		pi, ok := pos[a.strips[i].ID()]
		if !ok {
			pi = 1 << 20
		}
		pj, ok := pos[a.strips[j].ID()]
		if !ok {
			pj = 1 << 20
		}
		return pi < pj
	})
	// stereo links follow the restored parameters
	for _, s := range a.strips {
		s.Partner, s.LinkedBy = nil, nil
	}
	for _, s := range a.strips {
		if g, ok := u.gains[s.ID()]; ok {
			s.lastGain = g
		}
		if v, ok := s.param("StereoLink"); ok && strings.EqualFold(v, "true") {
			if r := a.right(s); r != nil && r.Kind == s.Kind && r.Partner == nil && r.LinkedBy == nil && s.LinkedBy == nil {
				s.Partner, r.LinkedBy = r, s
			}
		}
	}
	a.updatePositions()
	a.phantomOff = append([]string(nil), u.phantomOff...)
	a.phantomKilled = u.phantomOn
	a.savePhantomLocked()
	a.sessionDirty = true
}

// recordLocked stores the state before a change. Repeated changes of the same
// fader within a moment are one step, so a fader move is undone in one go.
func (a *App) recordLocked(key, desc string, coalesce bool) {
	now := time.Now()
	if coalesce && key == a.undoKey && now.Sub(a.undoAt) < 1500*time.Millisecond && len(a.undo) > 0 {
		a.undoAt = now
		return
	}
	a.undo = append(a.undo, a.captureLocked(desc))
	if len(a.undo) > undoDepth {
		a.undo = a.undo[len(a.undo)-undoDepth:]
	}
	a.redo = nil
	a.undoKey, a.undoAt = key, now
	if !coalesce {
		a.undoKey = ""
	}
	a.sessionDirty = true
}

func (a *App) clearUndoLocked() {
	a.undo, a.redo, a.undoKey = nil, nil, ""
}

func (a *App) undoLocked() string {
	if len(a.undo) == 0 {
		return ""
	}
	u := a.undo[len(a.undo)-1]
	a.undo = a.undo[:len(a.undo)-1]
	cur := a.captureLocked(u.desc)
	a.redo = append(a.redo, cur)
	a.restoreLocked(u)
	a.undoKey = ""
	a.setStatus("info", "Undo "+u.desc)
	return u.desc
}

func (a *App) redoLocked() string {
	if len(a.redo) == 0 {
		return ""
	}
	r := a.redo[len(a.redo)-1]
	a.redo = a.redo[:len(a.redo)-1]
	a.undo = append(a.undo, a.captureLocked(r.desc))
	a.restoreLocked(r)
	a.undoKey = ""
	a.setStatus("info", "Redo "+r.desc)
	return r.desc
}

var undoFieldNames = map[string]string{"gain": "gain", "phantom": "48 V", "hpf": "HPF", "insert": "insert", "softclip": "soft clip",
	"dcs": "DC servo", "cms": "CM servo"}

// undoInfo describes an operation for the undo list; ok is false for
// operations that are not undoable (connection, settings, polling, MIDI).
func (a *App) undoInfo(op Op) (key, desc string, coalesce, ok bool) {
	name := func() string {
		if s := a.stripByID(op.Strip); s != nil {
			return s.FixedLabel()
		}
		return ""
	}
	slot := fmt.Sprintf("slot %d", op.Slot+1)
	switch op.Op {
	case "mic", "dl5":
		f := undoFieldNames[op.Field]
		if f == "" {
			f = op.Field
		}
		return op.Op + op.Strip + op.Field, f + " " + name(), op.Field == "gain", true
	case "resetGain":
		return "", "reset gain " + name(), false, true
	case "link":
		return "", "stereo link " + name(), false, true
	case "label":
		return "", "name of " + name(), false, true
	case "comment":
		return "", "comment on " + name(), false, true
	case "color":
		return "", "colour of " + name(), false, true
	case "move", "moveTo":
		return "", "move " + name(), false, true
	case "hide", "show":
		return "", "show/hide " + slot, false, true
	case "aes":
		return "", "AES settings " + slot, false, true
	case "gpo", "gpoAll":
		return "", "GPO " + slot, false, true
	case "cmdByte":
		return "", "command byte " + slot, false, true
	case "rackLabel":
		return "", "rack name", false, true
	case "takeNames":
		return "", "names from DAW", false, true
	case "resetNames":
		return "", "reset names", false, true
	case "resetOrder":
		return "", "reset channel order", false, true
	case "phantomAll":
		return "", "48 V on all channels", false, true
	case "newSession":
		return "", "new session", false, true
	case "batch":
		var keys []string
		for _, o := range op.Ops {
			keys = append(keys, o.Op+o.Strip+o.Field)
		}
		c := len(op.Ops) > 0
		for _, o := range op.Ops {
			c = c && o.Field == "gain"
		}
		d := "gain"
		if len(op.Ops) > 0 {
			op0 := op.Ops[0]
			if s := a.stripByID(op0.Strip); s != nil {
				d = "gain " + s.FixedLabel()
			}
			if len(op.Ops) > 1 {
				d = fmt.Sprintf("gain of %d channels", len(op.Ops))
			}
		}
		return "batch" + strings.Join(keys, ","), d, c, true
	}
	return "", "", false, false
}

// ---- 48 V on all channels ----

func (a *App) phantomOf(s *Strip) (bool, bool) {
	switch s.Kind {
	case KindMic:
		return micDecode(s.Card, s.Ch, s.lastGain).Phantom, true
	case KindDL5:
		return dl5Decode(s.Card, s.Ch).Phantom, true
	}
	return false, false
}

func (a *App) setPhantom(s *Strip, on bool) {
	switch s.Kind {
	case KindMic:
		m := micDecode(s.Card, s.Ch, s.lastGain)
		m.Phantom = on
		s.setParam("Phantom", boolStr(on))
		micEncode(s.Card, s.Ch, m)
	case KindDL5:
		m := dl5Decode(s.Card, s.Ch)
		m.Phantom = on
		dl5Encode(s.Card, s.Ch, m)
	default:
		return
	}
	a.queueCard(s.Card)
}

// phantomAllLocked switches 48 V off on every channel of every card and
// remembers which channels had it; the next call switches exactly those on again.
func (a *App) phantomAllLocked() string {
	if a.phantomKilled {
		n := 0
		for _, id := range a.phantomOff {
			if s := a.stripByID(id); s != nil {
				a.setPhantom(s, true)
				n++
			}
		}
		a.phantomOff, a.phantomKilled = nil, false
		a.savePhantomLocked()
		a.setStatus("info", fmt.Sprintf("48 V switched on again on %d channels", n))
		return ""
	}
	var on []*Strip
	for _, s := range a.strips {
		if p, ok := a.phantomOf(s); ok && p {
			on = append(on, s)
		}
	}
	if len(on) == 0 {
		return "48 V is already off on all channels."
	}
	a.phantomOff = nil
	for _, s := range on {
		a.setPhantom(s, false)
		a.phantomOff = append(a.phantomOff, s.ID())
	}
	a.phantomKilled = true
	a.savePhantomLocked()
	a.setStatus("info", fmt.Sprintf("48 V switched off on %d channels", len(on)))
	return ""
}

// phantomSwitchedOn: 48 V switched on by hand (app, plug-in, MIDI) after "48 V
// OFF" ends that state; the panel button offers "48 V OFF" again.
func (a *App) phantomSwitchedOn(on bool) {
	if on && a.phantomKilled {
		a.phantomOff, a.phantomKilled = nil, false
		a.savePhantomLocked()
	}
}

func (a *App) savePhantomLocked() {
	if a.phantomKilled {
		a.cfg.Set("Phantom_Restore", strings.Join(a.phantomOff, ","))
	} else {
		a.cfg.Delete("Phantom_Restore")
	}
	a.cfgDirty = true
}

// ---- names, comments, order ----

// TrackInfo is what a "21 Channel" plug-in reports: its DAW track name and
// the rack channel it controls.
type trackInfo struct{ strip, name string }

// SetPluginTrack records (or with conn<0 … removes) a plug-in's track name.
func (a *App) SetPluginTrack(conn int, strip, name string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if strip == "" && name == "" {
		delete(a.tracks, conn)
	} else {
		a.tracks[conn] = trackInfo{strip, strings.TrimSpace(name)}
	}
	a.changed()
}

// dawNamesLocked maps strip id -> track name for every plug-in that has both.
func (a *App) dawNamesLocked() map[string]string {
	out := map[string]string{}
	conns := make([]int, 0, len(a.tracks))
	for c := range a.tracks {
		conns = append(conns, c)
	}
	sort.Ints(conns) // later plug-ins win
	for _, c := range conns {
		t := a.tracks[c]
		if t.strip != "" && t.name != "" && a.stripByID(t.strip) != nil {
			out[t.strip] = t.name
		}
	}
	return out
}

func (a *App) takeNamesLocked() string {
	names := a.dawNamesLocked()
	if len(names) == 0 {
		return "No track names to take. Put the “21 Channel” plug-in on the DAW tracks and choose a rack channel in each."
	}
	for id, n := range names {
		a.stripByID(id).setParam("Label", n)
	}
	a.setStatus("info", fmt.Sprintf("Names of %d channels taken from the DAW", len(names)))
	return ""
}

func (a *App) resetNamesLocked() {
	for _, s := range a.strips {
		s.Card.Params.Delete(s.key("Label"))
	}
}

func defaultLess(x, y *Strip) bool {
	if x.Card.Slot != y.Card.Slot {
		return x.Card.Slot < y.Card.Slot
	}
	return x.Ch < y.Ch
}

func (a *App) orderChangedLocked() bool {
	return !sort.SliceIsSorted(a.strips, func(i, j int) bool { return defaultLess(a.strips[i], a.strips[j]) })
}

func (a *App) resetOrderLocked() {
	sort.SliceStable(a.strips, func(i, j int) bool { return defaultLess(a.strips[i], a.strips[j]) })
	a.updatePositions()
}

// SetComment is used by the plug-ins; it is undoable like a change in the app.
func (a *App) SetComment(strip, text string) error {
	_, err := a.Do(Op{Op: "comment", Strip: strip, Text: text})
	return err
}

// ---- session: names, colours, comments and order survive a restart ----

func (a *App) sessionPath() string {
	name := "Session.xml"
	if a.portName == DemoPort {
		name = "Session-demo.xml"
	}
	return filepath.Join(filepath.Dir(a.cfg.Path), name)
}

// sessionKeep lists the per-channel parameters taken over from the last
// session; the rack itself stays the authority for gain and switches.
var sessionKeep = []string{"Label", "Color", "Comment"}

func (a *App) loadSessionLocked() {
	a.session, a.sessionOrder = nil, nil
	racks, err := LoadSnapshotFile(a.sessionPath())
	if err != nil {
		return
	}
	for _, r := range racks {
		if r.ID == 0 {
			a.session = r
		}
	}
	if a.session == nil {
		return
	}
	if a.session.Label != "" {
		a.rack.Label = a.session.Label
	}
	type sp struct {
		id  string
		pos int
	}
	var ps []sp
	for slot, c := range a.session.Cards {
		c.Params.Each(func(k, v string) {
			var ch int
			if _, err := fmt.Sscanf(strings.ToUpper(k), "CH%d_POSITION", &ch); err == nil && strings.HasSuffix(strings.ToUpper(k), "_POSITION") {
				var p int
				if _, err := fmt.Sscan(v, &p); err == nil {
					ps = append(ps, sp{fmt.Sprintf("s%dc%d", slot, ch), p})
				}
			}
		})
	}
	sort.Slice(ps, func(i, j int) bool { return ps[i].pos < ps[j].pos })
	for _, p := range ps {
		a.sessionOrder = append(a.sessionOrder, p.id)
	}
}

// applySessionCardLocked copies names, colours, comments and visibility of a
// card that is back in the same slot.
func (a *App) applySessionCardLocked(c *Card) {
	if a.session == nil {
		return
	}
	sc := a.session.Cards[c.Slot]
	if sc == nil || sc.Type != c.Type {
		return
	}
	delete(a.session.Cards, c.Slot)
	c.Show = sc.Show
	sc.Params.Each(func(k, v string) {
		for _, keep := range sessionKeep {
			if strings.HasSuffix(strings.ToLower(k), "_"+strings.ToLower(keep)) {
				c.Params.Set(k, v)
			}
		}
	})
}

// applySessionOrderLocked restores the channel order once the first poll is done.
func (a *App) applySessionOrderLocked() {
	if len(a.sessionOrder) == 0 {
		return
	}
	pos := map[string]int{}
	for i, id := range a.sessionOrder {
		pos[id] = i
	}
	sort.SliceStable(a.strips, func(i, j int) bool {
		pi, oki := pos[a.strips[i].ID()]
		pj, okj := pos[a.strips[j].ID()]
		if oki != okj {
			return oki
		}
		return oki && pi < pj
	})
	a.updatePositions()
	a.sessionOrder = nil
}

func (a *App) saveSessionLocked() {
	if a.portName == "" || a.rack.Module == 0xFFFF || a.rack.Module == 0 {
		return
	}
	hasCards := false
	for _, c := range a.rack.Cards {
		hasCards = hasCards || c.Type != NoCard
	}
	if !hasCards {
		return
	}
	if err := SaveSnapshotFile(a.sessionPath(), []*Rack{a.rack}); err != nil {
		log.Printf("save session: %v", err)
	}
}

// newSessionLocked starts from defaults: the microphone channels go to
// minimum gain with 48 V, HPF, insert and soft clip off (DC and CM servo of
// DL5 channels stay as they are), and names, comments, colours, order,
// visibility, stereo links and the rack name are reset.
func (a *App) newSessionLocked() {
	for _, s := range a.strips {
		for _, k := range []string{"Label", "Comment", "Color", "StereoLink"} {
			s.Card.Params.Delete(s.key(k))
		}
		s.Partner, s.LinkedBy = nil, nil
	}
	for _, s := range a.strips {
		switch s.Kind {
		case KindMic:
			a.setMicField(s, "gain", MicGainMin)
			for _, f := range []string{"phantom", "hpf", "insert", "softclip"} {
				a.setMicField(s, f, false)
			}
		case KindDL5:
			a.setDL5Field(s, "gain", DL5GainMin)
			a.setDL5Field(s, "phantom", false)
			a.setDL5Field(s, "hpf", false)
		}
	}
	a.phantomOff, a.phantomKilled = nil, false
	a.savePhantomLocked()
	for i, c := range a.rack.Cards {
		c.Show = !(c.Type == MicOdd && i > 0 && a.rack.Cards[i-1].Type != MicEven)
	}
	a.rack.Label = "Rack 1"
	a.resetOrderLocked()
	a.setStatus("info", "New session")
}
