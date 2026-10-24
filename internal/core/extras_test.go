// SPDX-License-Identifier: AGPL-3.0-or-later
package core

import (
	"path/filepath"
	"testing"
)

func micState(a *App, id string) MicState {
	for _, s := range a.State().Strips {
		if s.ID == id {
			return *s.Mic
		}
	}
	panic("no strip " + id)
}

func stripView(a *App, id string) StripView {
	for _, s := range a.State().Strips {
		if s.ID == id {
			return s
		}
	}
	panic("no strip " + id)
}

func TestUndoRedo(t *testing.T) {
	a, _ := newTestApp(t)
	a.Poll()
	id := "s0c1"
	g0 := micState(a, id).Gain
	// a fader move made of many steps is one undo step
	for g := 10; g <= 30; g += 2 {
		a.Do(Op{Op: "mic", Strip: id, Field: "gain", Value: g})
	}
	a.Do(Op{Op: "mic", Strip: id, Field: "hpf", Value: true})
	a.Do(Op{Op: "label", Strip: id, Text: "Kick"})
	if st := a.State().Panel; !st.CanUndo || st.UndoText != "name of MIC 1/1" {
		t.Fatalf("panel %+v", st)
	}
	a.Do(Op{Op: "undo"})
	if stripView(a, id).Label != "" || !micState(a, id).HPF {
		t.Fatal("undo label")
	}
	a.Do(Op{Op: "undo"})
	if micState(a, id).HPF || micState(a, id).Gain != 30 {
		t.Fatalf("undo hpf: %+v", micState(a, id))
	}
	a.Do(Op{Op: "undo"})
	if micState(a, id).Gain != g0 {
		t.Fatalf("undo gain: %d want %d", micState(a, id).Gain, g0)
	}
	a.Do(Op{Op: "redo"})
	a.Do(Op{Op: "redo"})
	if m := micState(a, id); m.Gain != 30 || !m.HPF {
		t.Fatalf("redo: %+v", m)
	}
	// a new change clears redo
	a.Do(Op{Op: "move", Strip: id, Delta: 1})
	if a.State().Panel.CanRedo {
		t.Fatal("redo after new change")
	}
	if !a.State().Panel.OrderChanged {
		t.Fatal("order not marked changed")
	}
	a.Do(Op{Op: "undo"})
	if a.State().Panel.OrderChanged {
		t.Fatal("undo move")
	}
	// settings / connection are not undoable
	n := len(a.undo)
	a.Do(Op{Op: "trace", Value: true})
	if len(a.undo) != n {
		t.Fatal("trace recorded")
	}
}

func TestPhantomAllToggle(t *testing.T) {
	a, _ := newTestApp(t)
	a.Poll()
	a.Do(Op{Op: "mic", Strip: "s0c1", Field: "phantom", Value: true})
	a.Do(Op{Op: "mic", Strip: "s0c3", Field: "phantom", Value: true})
	before := a.State().Panel.PhantomOn
	if before < 2 {
		t.Fatalf("phantom on: %d", before)
	}
	a.Do(Op{Op: "phantomAll"})
	p := a.State().Panel
	if p.PhantomOn != 0 || !p.PhantomKilled || p.PhantomOff != before {
		t.Fatalf("after off: %+v", p)
	}
	if a.cfg.Get("Phantom_Restore") == "" {
		t.Fatal("restore list not kept")
	}
	// switching 48 V on by hand ends the "restore" state
	a.Do(Op{Op: "mic", Strip: "s0c4", Field: "phantom", Value: true})
	if p := a.State().Panel; p.PhantomKilled || a.cfg.Get("Phantom_Restore") != "" {
		t.Fatalf("manual 48 V did not reset the button: %+v", p)
	}
	a.Do(Op{Op: "undo"})
	if !a.State().Panel.PhantomKilled {
		t.Fatal("undo did not bring the restore state back")
	}
	a.Do(Op{Op: "phantomAll"})
	p = a.State().Panel
	if p.PhantomOn != before || p.PhantomKilled || !micState(a, "s0c1").Phantom || micState(a, "s0c2").Phantom {
		t.Fatalf("after restore: %+v", p)
	}
}

func TestResetOrderNamesComments(t *testing.T) {
	a, _ := newTestApp(t)
	a.Poll()
	a.Do(Op{Op: "move", Strip: "s0c1", Delta: 2})
	a.Do(Op{Op: "label", Strip: "s0c2", Text: "Tom"})
	a.Do(Op{Op: "comment", Strip: "s0c2", Text: "MD421"})
	if v := stripView(a, "s0c2"); v.Comment != "MD421" {
		t.Fatalf("comment %+v", v)
	}
	a.Do(Op{Op: "resetOrder"})
	if a.State().Panel.OrderChanged || a.State().Strips[0].ID != "s0c1" {
		t.Fatal("order not reset")
	}
	a.Do(Op{Op: "resetNames"})
	if stripView(a, "s0c2").Label != "" || stripView(a, "s0c2").Comment != "MD421" {
		t.Fatal("reset names")
	}
	// take names from plug-ins
	a.SetPluginTrack(1, "s0c3", "Bass DI")
	a.SetPluginTrack(2, "s0c4", "")
	if msg, _ := a.Do(Op{Op: "takeNames"}); msg != "" || stripView(a, "s0c3").Label != "Bass DI" || stripView(a, "s0c3").DAWName != "Bass DI" {
		t.Fatalf("take names: %q", msg)
	}
}

// Names, colours, comments and order come back after a restart; gain does not.
func TestSessionRestore(t *testing.T) {
	dir := t.TempDir()
	cfg := LoadConfig(filepath.Join(dir, "settings.xml"))
	a := NewApp(cfg)
	a.Do(Op{Op: "settings", Value: map[string]any{"port": DemoPort}})
	a.Poll()
	a.Do(Op{Op: "label", Strip: "s1c2", Text: "Vox"})
	a.Do(Op{Op: "comment", Strip: "s1c2", Text: "U87, pad off"})
	a.Do(Op{Op: "move", Strip: "s1c2", Delta: -3})
	order := a.State().Strips[0].ID
	a.Close()

	b := NewApp(LoadConfig(filepath.Join(dir, "settings.xml")))
	if !b.State().Panel.Idle {
		t.Fatal("app must not start demo by itself")
	}
	b.Do(Op{Op: "settings", Value: map[string]any{"port": DemoPort}})
	b.Poll()
	v := stripView(b, "s1c2")
	if v.Label != "Vox" || v.Comment != "U87, pad off" || b.State().Strips[0].ID != order {
		t.Fatalf("session: %+v first=%s want %s", v, b.State().Strips[0].ID, order)
	}
}

func TestNewSession(t *testing.T) {
	a, _ := newTestApp(t)
	a.Poll()
	a.Do(Op{Op: "mic", Strip: "s0c1", Field: "gain", Value: 40})
	a.Do(Op{Op: "label", Strip: "s0c1", Text: "Kick"})
	a.Do(Op{Op: "comment", Strip: "s0c1", Text: "D112"})
	a.Do(Op{Op: "color", Strip: "s0c1", Text: "#112233"})
	a.Do(Op{Op: "link", Strip: "s0c1", Value: true})
	a.Do(Op{Op: "move", Strip: "s0c1", Delta: 3})
	a.Do(Op{Op: "hide", Strip: "s1c1"})
	a.Do(Op{Op: "rackLabel", Text: "Stage"})
	a.Do(Op{Op: "newSession"})
	v := stripView(a, "s0c1")
	m := micState(a, "s0c1")
	if v.Label != "" || v.Comment != "" || v.Color == "#112233" || v.Linked || !stripView(a, "s1c1").Visible ||
		a.State().Panel.OrderChanged || a.State().Rack.Label != "Rack 1" || m.Gain != MicGainMin || m.Phantom || m.HPF ||
		a.State().Panel.PhantomOn != 0 {
		t.Fatalf("new session: %+v rack %q", v, a.State().Rack.Label)
	}
	a.Do(Op{Op: "undo"})
	if stripView(a, "s0c1").Label != "Kick" || a.State().Rack.Label != "Stage" || micState(a, "s0c1").Gain != 40 {
		t.Fatal("undo new session")
	}
}
