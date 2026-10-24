// SPDX-License-Identifier: AGPL-3.0-or-later
package core

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// Strip is one channel strip of the Channels view.
type Strip struct {
	Card     *Card
	Ch       int
	Kind     string
	Partner  *Strip // stereo-linked right neighbour (this strip is the master)
	LinkedBy *Strip // master of the link (this strip is disabled)
	lastGain int
}

func (s *Strip) ID() string { return fmt.Sprintf("s%dc%d", s.Card.Slot, s.Ch) }

func (s *Strip) key(name string) string { return fmt.Sprintf("CH%d_%s", s.Ch, name) }

func (s *Strip) param(name string) (string, bool) { return s.Card.Params.Get(s.key(name)) }

func (s *Strip) setParam(name, v string) { s.Card.Params.Set(s.key(name), v) }

func (s *Strip) position() int {
	v, ok := s.param("Position")
	if !ok {
		return -1
	}
	n, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil {
		return -1
	}
	return n
}

func (s *Strip) channelCount() int {
	switch s.Kind {
	case KindMic:
		return 4
	case KindDL5:
		return 8
	}
	return 0
}

func (s *Strip) single() bool { return s.Kind == KindMic || s.Kind == KindDL5 }

func (s *Strip) inFrom() int {
	if s.single() {
		return s.Card.InputID + s.Ch - 1
	}
	return s.Card.InputID
}

func (s *Strip) inTo() int {
	if s.single() {
		return s.inFrom()
	}
	return s.Card.InputID + s.Card.AudioIn*s.Card.mult() - 1
}

func (s *Strip) outFrom() int {
	if s.single() {
		return s.Card.OutputID + s.Ch - 1
	}
	return s.Card.OutputID
}

func (s *Strip) outTo() int {
	if s.single() {
		return s.outFrom()
	}
	return s.Card.OutputID + s.Card.AudioOut*s.Card.mult() - 1
}

// FixedLabel is the strip name from card type, slot and channel ("MIC 1/2").
func (s *Strip) FixedLabel() string {
	if s.channelCount() > 1 {
		return fmt.Sprintf("%s %d/%d", Acronym(s.Card.Type), s.Card.Slot+1, s.Ch)
	}
	return fmt.Sprintf("%s %d", Acronym(s.Card.Type), s.Card.Slot+1)
}

func (s *Strip) ChannelIDs() string {
	var b strings.Builder
	switch {
	case s.Card.AudioIn == 0:
		b.WriteString("-")
	case s.single():
		b.WriteString(strconv.Itoa(s.inFrom()))
	default:
		fmt.Fprintf(&b, "%d-%d", s.inFrom(), s.inTo())
	}
	b.WriteString("/")
	switch {
	case s.Card.AudioOut == 0:
		b.WriteString("-")
	case s.single():
		b.WriteString(strconv.Itoa(s.outFrom()))
	default:
		fmt.Fprintf(&b, "%d-%d", s.outFrom(), s.outTo())
	}
	return b.String()
}

func (s *Strip) Tooltip() string {
	c := s.Card
	in, out := c.Channels()
	return fmt.Sprintf("Card Type: %s\nSlot: %d\nChannel: %d\nInput Channels: %d\nOutput Channels: %d",
		Description(c.Type), c.Slot+1, s.Ch, in, out)
}

// Color returns the strip colour as #rrggbb (param CHn_Color holds a signed 32-bit ARGB value).
func (s *Strip) Color() string {
	if v, ok := s.param("Color"); ok {
		if n, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64); err == nil {
			u := uint32(n)
			return fmt.Sprintf("#%02x%02x%02x", (u>>16)&0xFF, (u>>8)&0xFF, u&0xFF)
		}
	}
	c := kindColor[s.Kind]
	return fmt.Sprintf("#%02x%02x%02x", c[0], c[1], c[2])
}

func argb(r, g, b int) string {
	return strconv.Itoa(int(int32(uint32(0xFF)<<24 | uint32(r)<<16 | uint32(g)<<8 | uint32(b))))
}

func (s *Strip) SetColorHex(hex string) error {
	hex = strings.TrimPrefix(strings.TrimSpace(hex), "#")
	if hex == "" { // back to the colour of the card type
		c := kindColor[s.Kind]
		s.setParam("Color", argb(c[0], c[1], c[2]))
		return nil
	}
	if len(hex) != 6 {
		return fmt.Errorf("bad colour %q", hex)
	}
	v, err := strconv.ParseUint(hex, 16, 32)
	if err != nil {
		return err
	}
	s.setParam("Color", argb(int(v>>16), int(v>>8&0xFF), int(v&0xFF)))
	return nil
}

// ---- strip list ----

func (a *App) stripByID(id string) *Strip {
	for _, s := range a.strips {
		if s.ID() == id {
			return s
		}
	}
	return nil
}

func (a *App) stripsOf(c *Card) []*Strip {
	var out []*Strip
	for _, s := range a.strips {
		if s.Card == c {
			out = append(out, s)
		}
	}
	return out
}

func (a *App) indexOf(s *Strip) int {
	for i, x := range a.strips {
		if x == s {
			return i
		}
	}
	return -1
}

func (a *App) updatePositions() {
	for i, s := range a.strips {
		s.setParam("Position", strconv.Itoa(i))
	}
}

// balancePositions orders the strips by their stored positions.
func (a *App) balancePositions() {
	type ps struct {
		s   *Strip
		pos int
		idx int
	}
	var with, without []ps
	for i, s := range a.strips {
		p := s.position()
		if p < 0 {
			without = append(without, ps{s, p, i})
		} else {
			with = append(with, ps{s, p, i})
		}
	}
	sort.SliceStable(with, func(i, j int) bool { return with[i].pos < with[j].pos })
	out := make([]*Strip, 0, len(a.strips))
	for _, x := range with {
		out = append(out, x.s)
	}
	for _, x := range without {
		out = append(out, x.s)
	}
	a.strips = out
	a.updatePositions()
}

func (a *App) resetPositions() {
	for _, s := range a.strips {
		s.Card.Params.Delete(s.key("Position"))
	}
}

// addStrips adds the strips of a card that appeared.
func (a *App) addStrips(c *Card) {
	if len(a.stripsOf(c)) > 0 || c.Type == NoCard {
		return
	}
	kind, n := stripLayout(c.Type)
	switch kind {
	case KindMic:
		c.setCommandSize(MicCommandSize)
	case KindDL5:
		c.setCommandSize(DL5CommandSize)
	}
	var created []*Strip
	for ch := 1; ch <= n; ch++ {
		s := &Strip{Card: c, Ch: ch, Kind: kind}
		if _, ok := s.param("Color"); !ok {
			col := kindColor[kind]
			s.setParam("Color", argb(col[0], col[1], col[2]))
		}
		a.strips = append(a.strips, s)
		created = append(created, s)
	}
	a.updatePositions()
	// The strips decode the command field into their controls
	// and their change handlers write the normalised value straight back.
	before := append([]byte(nil), c.Command...)
	for _, s := range created {
		switch kind {
		case KindMic:
			m := micDecode(c, s.Ch, 0)
			s.lastGain = m.Gain
			micEncode(c, s.Ch, m)
		case KindDL5:
			dl5Encode(c, s.Ch, dl5Decode(c, s.Ch))
		}
	}
	if string(before) != string(c.Command) {
		a.queueCard(c)
	}
	for _, s := range created {
		if v, ok := s.param("StereoLink"); ok && strings.EqualFold(v, "true") {
			a.stereoLink(s, true)
		}
	}
}

func (a *App) removeStrips(c *Card) {
	mine := a.stripsOf(c)
	if len(mine) == 0 {
		return
	}
	for _, s := range mine {
		if s.Partner != nil {
			s.Partner.LinkedBy = nil
			s.Partner = nil
		}
		if s.LinkedBy != nil {
			s.LinkedBy.Partner = nil
			s.LinkedBy.setParam("StereoLink", "False")
			s.LinkedBy = nil
		}
	}
	out := a.strips[:0]
	for _, s := range a.strips {
		if s.Card != c {
			out = append(out, s)
		}
	}
	a.strips = out
	a.balancePositions()
}

// moveRel moves a strip left or right; hidden strips are skipped.
func (a *App) moveRel(s *Strip, rel int) {
	i := a.indexOf(s)
	if i < 0 || rel == 0 {
		return
	}
	step := 1
	if rel < 0 {
		step = -1
	}
	for {
		j := i + rel
		if j < 0 || j >= len(a.strips) {
			return
		}
		if a.strips[j].Card.Show {
			break
		}
		rel += step
	}
	a.strips = append(a.strips[:i], a.strips[i+1:]...)
	j := i + rel
	if j > len(a.strips) {
		j = len(a.strips)
	}
	a.strips = append(a.strips[:j], append([]*Strip{s}, a.strips[j:]...)...)
	a.updatePositions()
}

// moveTo moves a strip next to another one (drag & drop).
func (a *App) moveTo(src, target *Strip, before bool) {
	from := a.indexOf(src)
	to := a.indexOf(target)
	if from < 0 || to < 0 || src == target {
		return
	}
	a.strips = append(a.strips[:from], a.strips[from+1:]...)
	if from < to {
		to--
	}
	if !before {
		to++
	}
	a.strips = append(a.strips[:to], append([]*Strip{src}, a.strips[to:]...)...)
	a.updatePositions()
}

func (a *App) right(s *Strip) *Strip {
	i := a.indexOf(s)
	if i < 0 || i+1 >= len(a.strips) {
		return nil
	}
	return a.strips[i+1]
}

// stereoLink links a strip with its right neighbour or unlinks it.
func (a *App) stereoLink(s *Strip, on bool) bool {
	if s.Kind != KindMic && s.Kind != KindDL5 {
		return false
	}
	if !on {
		if s.Partner != nil {
			s.Partner.LinkedBy = nil
			s.Partner = nil
		}
		s.setParam("StereoLink", "False")
		return true
	}
	if s.Partner != nil {
		return true
	}
	r := a.right(s)
	if r == nil || r.Kind != s.Kind || r.Partner != nil || r.LinkedBy != nil || s.LinkedBy != nil {
		s.setParam("StereoLink", "False")
		return false
	}
	s.Partner = r
	r.LinkedBy = s
	s.setParam("StereoLink", "True")
	// push the master's values to the slave
	switch s.Kind {
	case KindMic:
		m := micDecode(s.Card, s.Ch, s.lastGain)
		for _, f := range []struct {
			n string
			v any
		}{{"gain", m.Gain}, {"softclip", m.Softclip}, {"phantom", m.Phantom}, {"hpf", m.HPF}, {"insert", m.Insert}} {
			a.setMicField(r, f.n, f.v)
		}
	case KindDL5:
		m := dl5Decode(s.Card, s.Ch)
		a.setDL5Field(r, "gain", m.Gain)
		a.setDL5Field(r, "phantom", m.Phantom)
		a.setDL5Field(r, "hpf", m.HPF)
	}
	return true
}

func boolStr(b bool) string {
	if b {
		return "True"
	}
	return "False"
}

func asInt(v any) int {
	switch x := v.(type) {
	case int:
		return x
	case float64:
		return int(x)
	case bool:
		if x {
			return 1
		}
	}
	return 0
}

func asBool(v any) bool {
	switch x := v.(type) {
	case bool:
		return x
	case float64:
		return x != 0
	case int:
		return x != 0
	}
	return false
}

// setMicField changes one control of a mic strip and mirrors it to a linked slave.
func (a *App) setMicField(s *Strip, field string, v any) error {
	m := micDecode(s.Card, s.Ch, s.lastGain)
	switch field {
	case "gain":
		m.Gain = clamp(asInt(v), MicGainMin, MicGainMax)
		s.lastGain = m.Gain
		s.setParam("FaderValue", strconv.Itoa(m.Gain))
	case "phantom":
		m.Phantom = asBool(v)
		s.setParam("Phantom", boolStr(m.Phantom))
		a.phantomSwitchedOn(m.Phantom)
	case "hpf":
		m.HPF = asBool(v)
		s.setParam("Highpass", boolStr(m.HPF))
	case "insert":
		m.Insert = asBool(v)
		s.setParam("Insert", boolStr(m.Insert))
	case "softclip":
		m.Softclip = asBool(v)
		s.setParam("Softclip", boolStr(m.Softclip))
	default:
		return fmt.Errorf("unknown mic field %q", field)
	}
	micEncode(s.Card, s.Ch, m)
	a.queueCard(s.Card)
	if s.Partner != nil {
		return a.setMicField(s.Partner, field, v)
	}
	return nil
}

func (a *App) setDL5Field(s *Strip, field string, v any) error {
	m := dl5Decode(s.Card, s.Ch)
	switch field {
	case "gain":
		m.Gain = clamp(asInt(v), DL5GainMin, DL5GainMax)
	case "phantom":
		m.Phantom = asBool(v)
		a.phantomSwitchedOn(m.Phantom)
	case "hpf":
		m.HPF = asBool(v)
	case "cms":
		m.CMS = asBool(v)
	case "dcs":
		m.DCS = asBool(v)
	default:
		return fmt.Errorf("unknown dl5 field %q", field)
	}
	dl5Encode(s.Card, s.Ch, m)
	a.queueCard(s.Card)
	if s.Partner != nil {
		return a.setDL5Field(s.Partner, field, v)
	}
	return nil
}
