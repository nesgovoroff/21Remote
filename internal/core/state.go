// SPDX-License-Identifier: AGPL-3.0-or-later
package core

import (
	"fmt"
	"net"
	"path/filepath"
	"strings"

	"remote21/internal/proto"
	"remote21/internal/serial"
)

type SlotView struct {
	Slot      int    `json:"slot"`
	Type      int    `json:"type"`
	TypeName  string `json:"typeName"`
	Acronym   string `json:"acronym"`
	Desc      string `json:"desc"`
	Width     int    `json:"width"`
	Image     string `json:"image"`
	Show      bool   `json:"show"`
	Enabled   bool   `json:"enabled"`
	VoltageOK bool   `json:"voltageOK"`
	AudioIn   int    `json:"ain"`
	AudioOut  int    `json:"aout"`
	I8S       bool   `json:"i8s"`
	CmdSize   int    `json:"cmdSize"`
	StatSize  int    `json:"statSize"`
	Tooltip   string `json:"tooltip"`
}

type StripView struct {
	ID      string       `json:"id"`
	Slot    int          `json:"slot"`
	Ch      int          `json:"ch"`
	Kind    string       `json:"kind"`
	Fixed   string       `json:"fixed"`
	IDs     string       `json:"ids"`
	Label   string       `json:"label"`
	Comment string       `json:"comment"`
	DAWName string       `json:"dawName,omitempty"` // track name from a 21 Channel plug-in
	Color   string       `json:"color"`
	Enabled bool         `json:"enabled"`
	Visible bool         `json:"visible"`
	Linked  bool         `json:"linked"` // master of a stereo link
	Tooltip string       `json:"tooltip"`
	Mic     *MicState    `json:"mic,omitempty"`
	DL5     *DL5State    `json:"dl5,omitempty"`
	AES     *AESState    `json:"aes,omitempty"`
	ADAT    *ADATState   `json:"adat,omitempty"`
	GPIO    *GPIOState   `json:"gpio,omitempty"`
	Generic *GenericView `json:"generic,omitempty"`
}

type GenericView struct {
	ID       int      `json:"id"`
	Voltage  string   `json:"voltage"`
	AudioIn  int      `json:"ain"`
	AudioOut int      `json:"aout"`
	CT       string   `json:"ct"`
	Command  []string `json:"command"`
	Status   []string `json:"status"`
}

type State struct {
	Version      uint64      `json:"version"`
	Port         string      `json:"port"`
	Ports        []string    `json:"ports"`
	PortError    string      `json:"portError"`
	PortStatus   string      `json:"portStatus"`
	LocalIPs     []string    `json:"localIPs"`
	Online       bool        `json:"online"`
	Polling      bool        `json:"polling"`
	PollBusy     bool        `json:"pollBusy"`
	Countdown    int         `json:"countdown"`
	PollInterval int         `json:"pollInterval"`
	Intervals    []int       `json:"intervals"`
	FaderStep    int         `json:"faderStep"`
	Trace        bool        `json:"trace"`
	Rack         RackView    `json:"rack"`
	Slots        []SlotView  `json:"slots"`
	Strips       []StripView `json:"strips"`
	Status       string      `json:"status"`
	StatusKind   string      `json:"statusKind"`
	Recent       []string    `json:"recent"`
	RecentNames  []string    `json:"recentNames"`
	Limits       Limits      `json:"limits"`
	MIDI         MIDIView    `json:"midi"`
	Update       any         `json:"update"`
	Panel        PanelView   `json:"panel"`
}

// PanelView feeds the status and control panel on the left.
type PanelView struct {
	CanUndo       bool   `json:"canUndo"`
	CanRedo       bool   `json:"canRedo"`
	UndoText      string `json:"undoText"`
	RedoText      string `json:"redoText"`
	PhantomKilled bool   `json:"phantomKilled"` // 48 V switched off by the panel button
	PhantomOff    int    `json:"phantomOff"`    // channels it will switch on again
	PhantomOn     int    `json:"phantomOn"`     // channels with 48 V on now
	DAWNames      int    `json:"dawNames"`      // channels with a track name from the DAW
	Named         int    `json:"named"`         // channels with a user name
	OrderChanged  bool   `json:"orderChanged"`
	Idle          bool   `json:"idle"` // no connection chosen yet
}

type RackView struct {
	Label     string `json:"label"`
	Module    string `json:"module"`
	Wellness  string `json:"wellness"`
	Healthy   bool   `json:"healthy"`
	Message   string `json:"message"`
	Connected bool   `json:"connected"`
}

type Limits struct {
	MicMin, MicMax, MicRange int
	DL5Min, DL5Max, DL5Range int
	FsOut                    []string
	Wordlength               []string
}

func hex2(b []byte) []string {
	out := make([]string, len(b))
	for i, x := range b {
		out[i] = fmt.Sprintf("%02X", x)
	}
	return out
}

// State is a snapshot of everything the UI shows.
func (a *App) State() State {
	a.mu.Lock()
	defer a.mu.Unlock()
	st := State{
		Version: a.ver, Port: a.portName, PortError: a.portErr, Online: a.link != nil && a.rack.Online,
		Polling: a.polling, PollBusy: a.pollBusy, Countdown: a.countdown, PollInterval: a.pollInterval(),
		Intervals: PollIntervals, FaderStep: a.faderStep(), Trace: a.trace,
		Status: a.status, StatusKind: a.statusKind, Recent: append([]string(nil), a.recent...),
		Limits: Limits{MicGainMin, MicGainMax, MicRangeLimit, DL5GainMin, DL5GainMax, DL5RangeLimit, FsOutNames, WordlengthNames},
	}
	for _, r := range a.recent {
		st.RecentNames = append(st.RecentNames, filepath.Base(r))
	}
	if addrs, err := net.InterfaceAddrs(); err == nil {
		for _, ad := range addrs {
			if ipn, ok := ad.(*net.IPNet); ok && ipn.IP.To4() != nil && !ipn.IP.IsLoopback() {
				st.LocalIPs = append(st.LocalIPs, ipn.IP.String())
			}
		}
	}
	ports := serial.List()
	st.Ports = append([]string{DemoPort}, ports...)
	if a.portName != "" && a.portName != DemoPort {
		found := false
		for _, p := range ports {
			found = found || p == a.portName
		}
		if !found {
			st.Ports = append(st.Ports, a.portName)
		}
	}
	if a.link != nil && a.link.port != nil {
		if sp, ok := a.link.port.(interface{ Status() string }); ok {
			st.PortStatus = sp.Status()
		}
	}
	r := a.rack
	rv := RackView{Label: r.Label, Wellness: "Healthy", Healthy: true}
	if a.link == nil || !r.Online {
		name := a.portName
		if name == "" {
			name = "Serial port"
		}
		rv.Message = name + " offline"
		if a.portName == "" {
			rv.Message = ""
		}
	}
	switch {
	case r.Module == proto.ModuleNone || r.Module == 0:
		rv.Module, rv.Wellness, rv.Healthy = "Not Connected", "", false
		if st.PortStatus != "" && !strings.HasPrefix(st.PortStatus, "Connected") {
			rv.Message = st.PortStatus
		} else if a.noAnswer && a.link != nil && a.sim == nil {
			rv.Message = noAnswerMessage(a.portName)
		}
	case r.Wellness >= 0 && r.Wellness&proto.WellnessSick != 0:
		rv.Module, rv.Wellness, rv.Healthy = proto.ModuleDescription(r.Module), "Sick", false
		rv.Message = "Invalid Configuration. Try reconfig"
		rv.Connected = true
	default:
		rv.Module = proto.ModuleDescription(r.Module)
		rv.Connected = true
		if r.Wellness < 0 {
			rv.Wellness = "Unknown"
		}
	}
	st.Rack = rv
	pv := PanelView{CanUndo: len(a.undo) > 0, CanRedo: len(a.redo) > 0, PhantomKilled: a.phantomKilled,
		OrderChanged: a.orderChangedLocked(), Idle: a.portName == ""}
	if pv.CanUndo {
		pv.UndoText = a.undo[len(a.undo)-1].desc
	}
	if pv.CanRedo {
		pv.RedoText = a.redo[len(a.redo)-1].desc
	}
	for _, id := range a.phantomOff {
		if a.stripByID(id) != nil {
			pv.PhantomOff++
		}
	}
	daw := a.dawNamesLocked()
	pv.DAWNames = len(daw)
	for _, s := range a.strips {
		if p, ok := a.phantomOf(s); ok && p {
			pv.PhantomOn++
		}
		if l, _ := s.param("Label"); l != "" {
			pv.Named++
		}
	}
	st.Panel = pv
	st.MIDI = a.midiViewLocked()
	if a.UpdateState != nil {
		st.Update = a.UpdateState()
	}
	for _, c := range r.Cards {
		chIn, chOut := c.Channels()
		sv := SlotView{Slot: c.Slot, Type: c.Type, TypeName: TypeName(c.Type), Acronym: Acronym(c.Type),
			Desc: Description(c.Type), Width: slotWidth(c.Type), Image: slotImage(c.Type), Show: c.Show,
			Enabled: true, VoltageOK: c.VoltageOK, AudioIn: chIn, AudioOut: chOut, I8S: c.I8S,
			CmdSize: c.CommandSize, StatSize: c.StatusSize}
		if c.Type == MicOdd && c.Slot > 0 && r.Cards[c.Slot-1].Type != MicEven {
			sv.Enabled = false
		}
		sv.Tooltip = fmt.Sprintf("Card type: %s\nDescription: %s\nSlot: %d\nInput channels: %d\nOutput channels: %d",
			Acronym(c.Type), Description(c.Type), c.Slot+1, chIn, chOut)
		st.Slots = append(st.Slots, sv)
	}
	for _, s := range a.strips {
		c := s.Card
		label, _ := s.param("Label")
		comment, _ := s.param("Comment")
		v := StripView{ID: s.ID(), Slot: c.Slot, Ch: s.Ch, Kind: s.Kind, Fixed: s.FixedLabel(), IDs: s.ChannelIDs(),
			Label: label, Comment: comment, DAWName: daw[s.ID()], Color: s.Color(), Enabled: s.LinkedBy == nil, Visible: c.Show, Linked: s.Partner != nil,
			Tooltip: s.Tooltip()}
		switch s.Kind {
		case KindMic:
			m := micDecode(c, s.Ch, s.lastGain)
			v.Mic = &m
		case KindDL5:
			m := dl5Decode(c, s.Ch)
			v.DL5 = &m
		case KindAES:
			m := aesDecode(c)
			v.AES = &m
		case KindADAT:
			m := adatDecode(c)
			v.ADAT = &m
		case KindGPIO:
			m := gpioDecode(c)
			v.GPIO = &m
		case KindGeneric:
			volt := "Fail"
			if c.VoltageOK {
				volt = "OK"
			}
			ct := "I2S"
			if c.I8S {
				ct = "I8S"
			}
			gin, gout := c.Channels()
			v.Generic = &GenericView{ID: c.Slot, Voltage: volt, AudioIn: gin, AudioOut: gout, CT: ct,
				Command: hex2(c.Command), Status: hex2(c.Status)}
		}
		st.Strips = append(st.Strips, v)
	}
	return st
}

func (a *App) Log() []LogEntry {
	a.mu.Lock()
	l := a.link
	a.mu.Unlock()
	if l == nil {
		return nil
	}
	return l.Log()
}

// PortLabel gives a friendlier name for a device path.
func PortLabel(p string) string {
	if p == DemoPort {
		return "Demo (simulated D21m)"
	}
	if strings.HasPrefix(p, "tcp-listen://") {
		a := strings.TrimPrefix(p, "tcp-listen://")
		return "network (TCP port " + a[strings.LastIndex(a, ":")+1:] + ")"
	}
	if strings.HasPrefix(p, "tcp://") {
		return "network " + strings.TrimPrefix(p, "tcp://")
	}
	return strings.TrimPrefix(p, "/dev/")
}

// noAnswerMessage explains a port that opens but gets no answer from the frame.
func noAnswerMessage(port string) string {
	if strings.HasPrefix(port, "tcp://") {
		return "Connected to the device server, but the rack does not answer. Make sure no other program uses this port of the device server " +
			"(also an older version of this app still running, or another computer), that the D21m is wired to the serial port you chose, " +
			"and that the port is set to 115200, 8 data bits, no parity, 1 stop bit, no flow control."
	}
	return "The port is open, but the rack does not answer. Check the cable and that no other program uses this port."
}
