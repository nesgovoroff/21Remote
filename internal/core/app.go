// SPDX-License-Identifier: AGPL-3.0-or-later
package core

import (
	"errors"
	"fmt"
	"log"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"remote21/internal/netport"
	"remote21/internal/proto"
	"remote21/internal/serial"
)

const DemoPort = "Demo"

// PollIntervals are the poll interval choices (seconds).
var PollIntervals = []int{1, 2, 3, 5, 10, 15, 30, 45, 60, 120, 300, 600}

// App is the controller: model + strips + polling + command queue.
type App struct {
	mu sync.Mutex

	rack   *Rack
	strips []*Strip
	cfg    *Config

	link     *Link
	sim      *SimPort
	portName string
	portErr  string

	polling   bool
	noAnswer  bool // the port is open, but the frame did not answer the last poll
	polls     int
	countdown int
	pollBusy  bool
	trace     bool

	queue []*Card

	status     string
	statusKind string
	statusAge  int

	recent []string

	midi *midiState

	// undo/redo (extras.go)
	undo, redo []*undoState
	undoKey    string
	undoAt     time.Time

	// 48 V on all channels: channels switched off by "48 V off", to switch on again
	phantomOff    []string
	phantomKilled bool

	// track names reported by the "21 Channel" plug-ins, per connection
	tracks map[int]trackInfo

	// names, colours, comments and order from the last session
	session      *SnapRack
	sessionOrder []string
	sessionDirty bool
	sessionAt    time.Time
	cfgDirty     bool

	subs map[chan struct{}]struct{}
	ver  uint64

	Opener func(name string) (serial.Port, error)

	// UpdateState supplies the "update" part of the UI state (set by main).
	UpdateState func() any
}

// Notify tells the UI that something outside the core changed.
func (a *App) Notify() {
	a.mu.Lock()
	a.changed()
	a.mu.Unlock()
}

func NewApp(cfg *Config) *App {
	a := &App{
		rack:   NewRack("Rack 1"),
		cfg:    cfg,
		subs:   map[chan struct{}]struct{}{},
		Opener: openPort,
		tracks: map[int]trackInfo{},
	}
	if v := cfg.Get("Phantom_Restore"); v != "" {
		a.phantomOff, a.phantomKilled = strings.Split(v, ","), true
	}
	for i := 0; i < 5; i++ {
		if v := cfg.Get("Snapshots_Recent_" + strconv.Itoa(i)); v != "" {
			a.recent = append(a.recent, v)
		}
	}
	a.countdown = a.pollInterval()
	a.midiInit(filepath.Dir(cfg.Path))
	return a
}

func (a *App) pollInterval() int { return a.cfg.Int("COM_PollInterval", 5) }
func (a *App) faderStep() int    { return clamp(a.cfg.Int("Fader_StepSize", 2), 1, 10) }

// ---- change notification (SSE) ----

func (a *App) Subscribe() (chan struct{}, func()) {
	ch := make(chan struct{}, 1)
	a.mu.Lock()
	a.subs[ch] = struct{}{}
	a.mu.Unlock()
	return ch, func() {
		a.mu.Lock()
		delete(a.subs, ch)
		a.mu.Unlock()
	}
}

// changed must be called with a.mu held.
func (a *App) changed() {
	a.ver++
	for ch := range a.subs {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}

func (a *App) setStatus(kind, text string) {
	a.status, a.statusKind, a.statusAge = text, kind, 0
}

// ---- port handling ----

// Start opens the configured connection and starts the timers. Demo mode is
// never started on its own: without a saved connection (or after demo) the app
// shows an empty rack and waits for the user to choose Demo or Connect.
func (a *App) Start() {
	port := a.cfg.Get("COM_Port")
	a.mu.Lock()
	if port != "" && port != DemoPort {
		a.openPortLocked(port)
		a.polling = a.link != nil
	}
	a.mu.Unlock()
	go a.timerLoop()
	go a.senderLoop()
	go a.Poll()
}

// openPort opens a local serial device or a network (device server) port.
func openPort(name string) (serial.Port, error) {
	if netport.IsNet(name) {
		return netport.Open(name)
	}
	return serial.Open(name, 115200)
}

func (a *App) openPortLocked(name string) {
	if a.link != nil {
		a.link.Close()
		a.link = nil
	}
	if name != a.portName {
		// another rack (or the simulator): start from an empty frame
		a.saveSessionLocked()
		for i, c := range a.rack.Cards {
			a.setCardTypeLocked(c, NoCard)
			a.rack.Cards[i] = newCard(i, NoCard)
		}
		a.rack.UpdateChannelIds()
		a.rack.Module = 0xFFFF
		a.rack.Label = "Rack 1"
		a.setStatus("", "")
		a.clearUndoLocked()
		a.portName = name
		a.loadSessionLocked()
	}
	a.sim = nil
	a.portName = name
	a.portErr = ""
	a.noAnswer = false
	var p serial.Port
	var err error
	if name == DemoPort {
		a.sim = NewDemoSim()
		p = a.sim
	} else {
		p, err = a.Opener(name)
	}
	if err != nil {
		a.portErr = fmt.Sprintf("Could not open '%s': %v. Stop polling.", PortLabel(name), err)
		a.setStatus("error", a.portErr)
		log.Print(a.portErr)
		a.rack.Online = false
		a.polling = false
		a.changed()
		return
	}
	a.link = NewLink(p)
	if netport.IsNet(name) {
		a.link.timeout = 500 * time.Millisecond // network + device-server packing delay
	}
	a.link.SetTrace(a.trace)
	a.changed()
}

func (a *App) Close() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.saveSessionLocked()
	if a.link != nil {
		a.link.Close()
		a.link = nil
	}
}

// ---- timers: polling countdown and the command sender ----

func (a *App) timerLoop() {
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for range t.C {
		a.mu.Lock()
		if a.status != "" {
			a.statusAge++
			if a.statusAge > 10 && a.statusKind != "error" {
				a.status, a.statusKind = "", ""
			}
		}
		doPoll := false
		if a.polling {
			if a.countdown > 0 {
				a.countdown--
			} else {
				a.countdown = a.pollInterval()
				doPoll = true
			}
		}
		if a.sessionDirty && time.Since(a.sessionAt) > 2*time.Second {
			a.sessionDirty, a.sessionAt = false, time.Now()
			a.saveSessionLocked()
		}
		if a.cfgDirty {
			a.cfgDirty = false
			go a.cfg.Save()
		}
		a.changed()
		a.mu.Unlock()
		if doPoll {
			a.Poll()
		}
	}
}

func (a *App) queueCard(c *Card) {
	if c.Type == NoCard {
		return
	}
	for _, q := range a.queue {
		if q == c {
			return
		}
	}
	a.queue = append(a.queue, c)
}

func (a *App) senderLoop() {
	t := time.NewTicker(25 * time.Millisecond)
	defer t.Stop()
	for range t.C {
		a.mu.Lock()
		if len(a.queue) == 0 || a.link == nil {
			a.mu.Unlock()
			continue
		}
		c := a.queue[0]
		a.queue = a.queue[1:]
		slot, cmd, link := c.Slot, append([]byte(nil), c.Command...), a.link
		a.mu.Unlock()
		if len(cmd) > 0 {
			link.SetCommand(slot, cmd)
		}
	}
}

// flushQueue sends everything pending right away (used before snapshot save/quit).
func (a *App) FlushQueue() {
	for i := 0; i < 100; i++ {
		a.mu.Lock()
		n := len(a.queue)
		a.mu.Unlock()
		if n == 0 {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
}

// ---- polling ----

func (a *App) Poll() {
	a.mu.Lock()
	if a.pollBusy {
		a.mu.Unlock()
		return
	}
	a.pollBusy = true
	link := a.link
	a.changed()
	a.mu.Unlock()
	defer func() {
		a.mu.Lock()
		a.pollBusy = false
		if a.polls == 0 && (a.statusKind == "new" || a.statusKind == "removed") {
			a.status, a.statusKind = "", "" // cards found at start-up are not news
		}
		if a.rack.Online && a.rack.Module != proto.ModuleNone && a.rack.Module != 0 {
			a.applySessionOrderLocked()
		}
		a.polls++
		a.changed()
		a.mu.Unlock()
	}()

	if link == nil {
		a.mu.Lock()
		a.rack.Online = false
		a.polling = false
		a.changed()
		a.mu.Unlock()
		return
	}
	mt, err := link.ModuleType(0)
	if err != nil {
		mt = proto.ModuleNone
	}
	w, err := link.Wellness(0)
	if err != nil {
		w = -1
	}
	a.mu.Lock()
	a.rack.Online = true
	a.noAnswer = mt == proto.ModuleNone
	if a.noAnswer {
		log.Printf("the frame does not answer on %s", PortLabel(a.portName))
	}
	a.setModuleLocked(mt)
	a.rack.Wellness = w
	a.changed()
	a.mu.Unlock()
	if mt == proto.ModuleNone {
		return
	}
	for slot := 0; slot < proto.SlotsPerFrame; slot++ {
		st, err := link.SlotStatus(slot)
		var devErr *proto.DeviceError
		if errors.As(err, &devErr) {
			continue // an error answer from the frame leaves the slot as it is
		}
		a.mu.Lock()
		if err != nil {
			a.setCardTypeLocked(a.rack.Cards[slot], NoCard)
			a.changed()
			a.mu.Unlock()
			continue
		}
		// The card goes to the slot that was asked for. A double-width card
		// also answers for its second slot (with its own slot number): that
		// slot stays empty, the card is drawn across both.
		idx := slot
		if st.Slot != slot {
			if prev := slot - 1; prev >= 0 && st.Slot == prev && slotWidth(st.CardType) == 2 && a.rack.Cards[prev].Type == st.CardType {
				if a.rack.Cards[slot].Type != NoCard {
					a.setCardTypeLocked(a.rack.Cards[slot], NoCard)
				}
				a.changed()
				a.mu.Unlock()
				continue
			}
			link.addLog("info", fmt.Sprintf("slot %d answered as slot %d; the card is shown in slot %d", slot+1, st.Slot+1, slot+1))
		}
		c := a.rack.Cards[idx]
		c.I8S = st.I8S
		c.VoltageOK = st.VoltageOK
		c.setAudioIn(st.AudioIn)
		c.AudioOut = st.AudioOut
		if c.CommandSize != st.CommandSize {
			c.setCommandSize(st.CommandSize)
		}
		c.setStatusSize(st.StatusSize)
		copy(c.Status, st.Status)
		isNew := c.Type != st.CardType
		a.rack.UpdateChannelIds()
		a.mu.Unlock()

		var cmd []byte
		if isNew && st.CardType != NoCard && st.CommandSize > 0 {
			// a new card: read its current command field
			cmd, _ = link.Command(idx)
		}
		a.mu.Lock()
		if isNew {
			if cmd != nil && len(cmd) == c.CommandSize {
				copy(c.Command, cmd)
			}
			a.setCardTypeLocked(c, st.CardType)
		}
		a.changed()
		a.mu.Unlock()
	}
}

func (a *App) setModuleLocked(mt int) {
	if a.rack.Module == mt {
		return
	}
	a.rack.Module = mt
	if mt == proto.ModuleNone {
		for _, c := range a.rack.Cards {
			a.setCardTypeLocked(c, NoCard)
		}
	}
}

// setCardTypeLocked records a card change in a slot and updates the channel strips.
func (a *App) setCardTypeLocked(c *Card, t int) {
	if c.Type == t {
		return
	}
	a.removeStrips(c)
	c.setType(t)
	a.rack.UpdateChannelIds()
	if t == NoCard {
		a.setStatus("removed", fmt.Sprintf("Card in Slot %d removed", c.Slot+1))
	} else {
		a.setStatus("new", fmt.Sprintf("New Card in Slot %d: %s (%s)", c.Slot+1, Acronym(t), Description(t)))
		a.applySessionCardLocked(c)
		a.addStrips(c)
	}
	// odd microphone card only valid next to an even one
	for i := 1; i < 10; i += 2 {
		if a.rack.Cards[i].Type == MicOdd {
			a.rack.Cards[i].Show = a.rack.Cards[i-1].Type == MicEven
		}
	}
}

// ---- operations from the UI ----

type Op struct {
	Op     string `json:"op"`
	Strip  string `json:"strip"`
	Target string `json:"target"`
	Slot   int    `json:"slot"`
	Field  string `json:"field"`
	Index  int    `json:"index"`
	Value  any    `json:"value"`
	Before bool   `json:"before"`
	Text   string `json:"text"`
	Delta  int    `json:"delta"`
	Ops    []Op   `json:"ops"`

	noUndo bool
}

func (a *App) card(slot int) (*Card, error) {
	if slot < 0 || slot >= 12 {
		return nil, fmt.Errorf("bad slot %d", slot)
	}
	return a.rack.Cards[slot], nil
}

// Do applies one operation; it returns an optional message for the UI.
func (a *App) Do(op Op) (string, error) {
	if strings.HasPrefix(op.Op, "midi") {
		return a.midiOp(op)
	}
	switch op.Op {
	case "pollNow":
		go a.Poll()
		return "", nil
	case "polling":
		a.mu.Lock()
		on := asBool(op.Value)
		if on && a.link == nil {
			a.openPortLocked(a.portName)
		}
		a.polling = on && a.link != nil
		a.countdown = a.pollInterval()
		a.changed()
		a.mu.Unlock()
		if on {
			go a.Poll()
		}
		return "", nil
	case "settings":
		return a.applySettings(op)
	case "trace":
		a.mu.Lock()
		a.trace = asBool(op.Value)
		if a.link != nil {
			a.link.SetTrace(a.trace)
		}
		a.changed()
		a.mu.Unlock()
		return "", nil
	case "undo", "redo":
		a.mu.Lock()
		defer a.mu.Unlock()
		defer a.changed()
		if op.Op == "undo" {
			a.undoLocked()
		} else {
			a.redoLocked()
		}
		return "", nil
	case "batch":
		if !op.noUndo {
			a.mu.Lock()
			if key, desc, co, ok := a.undoInfo(op); ok {
				a.recordLocked(key, desc, co)
			}
			a.mu.Unlock()
		}
		for _, o := range op.Ops {
			o.noUndo = true
			if _, err := a.Do(o); err != nil {
				return "", err
			}
		}
		return "", nil
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	defer a.changed()
	if !op.noUndo {
		if key, desc, co, ok := a.undoInfo(op); ok {
			a.recordLocked(key, desc, co)
		}
	}
	switch op.Op {
	case "takeNames":
		return a.takeNamesLocked(), nil
	case "resetNames":
		a.resetNamesLocked()
		return "", nil
	case "resetOrder":
		a.resetOrderLocked()
		return "", nil
	case "phantomAll":
		return a.phantomAllLocked(), nil
	case "newSession":
		a.newSessionLocked()
		return "", nil
	case "clearRecent":
		a.recent = nil
		for i := 0; i < 5; i++ {
			a.cfg.Delete("Snapshots_Recent_" + strconv.Itoa(i))
		}
		a.cfgDirty = true
		return "", nil
	}
	var s *Strip
	if op.Strip != "" {
		if s = a.stripByID(op.Strip); s == nil {
			return "", fmt.Errorf("unknown strip %s", op.Strip)
		}
	}
	need := func() error {
		if s == nil {
			return errors.New("strip required")
		}
		if s.LinkedBy != nil {
			return errors.New("channel is stereo-linked")
		}
		return nil
	}
	switch op.Op {
	case "mic":
		if err := need(); err != nil {
			return "", err
		}
		return "", a.setMicField(s, op.Field, op.Value)
	case "dl5":
		if err := need(); err != nil {
			return "", err
		}
		return "", a.setDL5Field(s, op.Field, op.Value)
	case "resetGain":
		if err := need(); err != nil {
			return "", err
		}
		if s.Kind == KindMic {
			return "", a.setMicField(s, "gain", MicGainMin)
		}
		return "", a.setDL5Field(s, "gain", DL5GainMin)
	case "link":
		if err := need(); err != nil {
			return "", err
		}
		if !a.stereoLink(s, asBool(op.Value)) {
			return "Stereo link needs a free channel of the same type to the right.", nil
		}
		return "", nil
	case "label":
		if s == nil {
			return "", errors.New("strip required")
		}
		s.setParam("Label", op.Text)
		return "", nil
	case "comment":
		if s == nil {
			return "", errors.New("strip required")
		}
		if t := strings.TrimSpace(op.Text); t == "" {
			s.Card.Params.Delete(s.key("Comment"))
		} else {
			s.setParam("Comment", op.Text)
		}
		return "", nil
	case "color":
		if s == nil {
			return "", errors.New("strip required")
		}
		return "", s.SetColorHex(op.Text)
	case "move":
		if s == nil {
			return "", errors.New("strip required")
		}
		a.moveRel(s, op.Delta)
		return "", nil
	case "moveTo":
		t := a.stripByID(op.Target)
		if s == nil || t == nil {
			return "", errors.New("strip and target required")
		}
		a.moveTo(s, t, op.Before)
		return "", nil
	case "hide":
		if s == nil {
			return "", errors.New("strip required")
		}
		s.Card.Show = false
		return "", nil
	}

	c, err := a.card(op.Slot)
	if err != nil {
		return "", err
	}
	switch op.Op {
	case "show":
		if c.Type == MicOdd && op.Slot > 0 && a.rack.Cards[op.Slot-1].Type != MicEven {
			return "", nil
		}
		c.Show = asBool(op.Value)
	case "aes":
		switch op.Field {
		case "sfcIn":
			aesSetSFCIn(c, clamp(op.Index, 0, 7), asBool(op.Value))
		case "sfcOut":
			aesSetSFCOut(c, clamp(op.Index, 0, 1), asBool(op.Value))
		case "fsOut":
			aesSetFsOut(c, clamp(op.Index, 0, 1), asInt(op.Value))
		case "wordlength":
			aesSetWordlength(c, clamp(op.Index, 0, 7), clamp(asInt(op.Value), 0, 3))
		default:
			return "", fmt.Errorf("unknown aes field %q", op.Field)
		}
		a.queueCard(c)
	case "gpo":
		i := clamp(op.Index, 0, 15)
		c.setBit(i/8, i%8, asBool(op.Value))
		a.queueCard(c)
	case "gpoAll":
		for i := 0; i < 16; i++ {
			v := c.bit(i/8, i%8)
			switch op.Text {
			case "set":
				v = true
			case "clear":
				v = false
			case "invert":
				v = !v
			}
			c.setBit(i/8, i%8, v)
		}
		a.queueCard(c)
	case "cmdByte":
		c.setByte(op.Index, byte(clamp(asInt(op.Value), 0, 255)))
		a.queueCard(c)
	case "rackLabel":
		a.rack.Label = op.Text
	default:
		return "", fmt.Errorf("unknown op %q", op.Op)
	}
	return "", nil
}

func (a *App) applySettings(op Op) (string, error) {
	m, _ := op.Value.(map[string]any)
	if v, ok := m["pollInterval"]; ok {
		a.cfg.Set("COM_PollInterval", strconv.Itoa(clamp(asInt(v), 1, 600)))
	}
	if v, ok := m["faderStep"]; ok {
		a.cfg.Set("Fader_StepSize", strconv.Itoa(clamp(asInt(v), 1, 10)))
	}
	a.mu.Lock()
	a.countdown = a.pollInterval()
	msg := ""
	if v, ok := m["port"].(string); ok && v != "" && (v != a.portName || a.link == nil) {
		a.cfg.Set("COM_Port", v)
		a.openPortLocked(v)
		a.polling = a.link != nil
		msg = a.portErr
	}
	a.changed()
	a.mu.Unlock()
	if err := a.cfg.Save(); err != nil {
		log.Printf("save settings: %v", err)
	}
	go a.Poll()
	return msg, nil
}

// ---- snapshots ----

func (a *App) addRecentLocked(path string) {
	for _, r := range a.recent {
		if r == path {
			return
		}
	}
	a.recent = append(a.recent, path)
	if len(a.recent) > 5 {
		a.recent = a.recent[len(a.recent)-5:]
	}
	for i := 0; i < 5; i++ {
		k := "Snapshots_Recent_" + strconv.Itoa(i)
		if i < len(a.recent) {
			a.cfg.Set(k, a.recent[i])
		} else {
			a.cfg.Delete(k)
		}
	}
	if err := a.cfg.Save(); err != nil {
		log.Printf("save settings: %v", err)
	}
}

func (a *App) SaveSnapshot(path string) error {
	if !strings.HasSuffix(strings.ToLower(path), ".xml") {
		path += ".xml"
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := SaveSnapshotFile(path, []*Rack{a.rack}); err != nil {
		return fmt.Errorf("%v, could not save the session (%s)", err, path)
	}
	a.addRecentLocked(path)
	a.setStatus("info", "Session saved: "+filepath.Base(path))
	a.changed()
	return nil
}

// Mismatch describes a slot whose card type differs between snapshot and hardware.
type Mismatch struct {
	Slot     int    `json:"slot"`
	Snapshot string `json:"snapshot"`
	Now      string `json:"now"`
}

// OpenSnapshot loads a snapshot. When the snapshot
// does not match the hardware and force is false, it returns the mismatches
// without applying anything; with force, matching cards are applied and
// mismatching ones skipped.
func (a *App) OpenSnapshot(path string, force bool) ([]Mismatch, error) {
	racks, err := LoadSnapshotFile(path)
	if err != nil {
		return nil, fmt.Errorf("could not open the session (%s): %v", filepath.Base(path), err)
	}
	a.mu.Lock()
	polling := a.polling
	a.polling = false
	a.mu.Unlock()
	a.Poll()
	a.mu.Lock()
	defer func() {
		a.polling = polling
		a.countdown = a.pollInterval()
		a.changed()
		a.mu.Unlock()
	}()
	var snap *SnapRack
	for _, r := range racks {
		if r.ID == 0 {
			snap = r
		}
	}
	if snap == nil {
		return nil, errors.New("the session file contains no rack")
	}
	var mm []Mismatch
	for i, c := range a.rack.Cards {
		st := NoCard
		if sc := snap.Cards[i]; sc != nil {
			st = sc.Type
		}
		if st != c.Type {
			mm = append(mm, Mismatch{Slot: i + 1, Snapshot: TypeName(st), Now: TypeName(c.Type)})
		}
	}
	if len(mm) > 0 && !force {
		return mm, nil
	}
	a.recordLocked("", "open session "+filepath.Base(path), false)
	a.resetPositions()
	a.rack.Label = snap.Label
	for i, c := range a.rack.Cards {
		sc := snap.Cards[i]
		if sc == nil || sc.Type != c.Type {
			continue
		}
		c.Show = sc.Show
		if sc.Command != nil {
			c.setCommandSize(len(sc.Command))
			copy(c.Command, sc.Command)
		}
		sc.Params.Each(func(k, v string) { c.Params.Set(k, v) })
		a.queueCard(c)
	}
	a.balancePositions()
	// re-evaluate stereo links from the parameters
	for _, s := range a.strips {
		if s.Partner != nil {
			a.stereoLink(s, false)
		}
	}
	for _, s := range a.strips {
		if v, ok := s.param("StereoLink"); ok && strings.EqualFold(v, "true") {
			a.stereoLink(s, true)
		}
		if s.Kind == KindMic {
			s.lastGain = micDecode(s.Card, s.Ch, s.lastGain).Gain
		}
	}
	a.addRecentLocked(path)
	a.setStatus("info", "Session loaded: "+filepath.Base(path))
	return nil, nil
}

func (a *App) Recent(i int) (string, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if i < 0 || i >= len(a.recent) {
		return "", false
	}
	return a.recent[i], true
}

// Port is the chosen connection ("" when none, "Demo", a device or tcp://…).
func (a *App) Port() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.portName
}

// SimPort exposes the demo simulator (nil when a real port is used).
func (a *App) Sim() *SimPort {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.sim
}
