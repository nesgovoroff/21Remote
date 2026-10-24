// SPDX-License-Identifier: AGPL-3.0-or-later
package core

import (
	"encoding/xml"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"
)

// SnapCard / SnapRack is a snapshot as read from disk (SnapshotManager).
type SnapCard struct {
	Slot    int
	Type    int
	Show    bool
	Command []byte // nil when the file has no commandField
	Params  *Params
}

type SnapRack struct {
	ID    int
	Label string
	Cards map[int]*SnapCard // by 0-based slot
}

// WriteSnapshot writes a snapshot file (XML).
func WriteSnapshot(w io.Writer, racks []*Rack) error {
	var b strings.Builder
	now := time.Now()
	b.WriteString("<?xml version=\"1.0\" encoding=\"utf-8\"?>\n")
	b.WriteString("<!-- This is a snapshot file for 21 Remote -->\n")
	fmt.Fprintf(&b, "<D21mSnapshot date=\"%s\" time=\"%s\">\n", now.Format("02.01.2006"), now.Format("15:04"))
	b.WriteString("  <D21mSystem>\n")
	for id, r := range racks {
		fmt.Fprintf(&b, "    <Rack name=\"%s\" id=\"%d\">\n", xmlEsc(r.Label), id)
		for _, c := range r.Cards {
			if c.Type == NoCard {
				continue
			}
			fmt.Fprintf(&b, "      <Card slot=\"%d\" type=\"%s\" showInChannelView=\"%s\" commandField=\"%s\">\n",
				c.Slot+1, TypeName(c.Type), boolStr(c.Show), c.CommandString())
			c.Params.Each(func(k, v string) {
				fmt.Fprintf(&b, "        <Parameter key=\"%s\" value=\"%s\" />\n", xmlEsc(k), xmlEsc(v))
			})
			b.WriteString("      </Card>\n")
		}
		b.WriteString("    </Rack>\n")
	}
	b.WriteString("  </D21mSystem>\n</D21mSnapshot>\n")
	_, err := io.WriteString(w, b.String())
	return err
}

func SaveSnapshotFile(path string, racks []*Rack) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	if err := WriteSnapshot(f, racks); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

func attr(se xml.StartElement, name string) (string, bool) {
	for _, a := range se.Attr {
		if strings.EqualFold(a.Name.Local, name) {
			return a.Value, true
		}
	}
	return "", false
}

// ReadSnapshot parses a snapshot (lenient: unknown elements are skipped).
func ReadSnapshot(r io.Reader) ([]*SnapRack, error) {
	dec := xml.NewDecoder(r)
	dec.CharsetReader = func(label string, in io.Reader) (io.Reader, error) { return in, nil }
	var racks []*SnapRack
	var rack *SnapRack
	var card *SnapCard
	sawRoot := false
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			line, _ := dec.InputPos()
			return nil, fmt.Errorf("syntax error on line %d: %v", line, err)
		}
		se, ok := tok.(xml.StartElement)
		if !ok {
			continue
		}
		switch strings.ToLower(se.Name.Local) {
		case "d21msnapshot":
			sawRoot = true
		case "rack":
			id := 0
			if v, ok := attr(se, "id"); ok {
				if id, err = strconv.Atoi(strings.TrimSpace(v)); err != nil {
					return nil, fmt.Errorf("bad rack id %q", v)
				}
			}
			name, _ := attr(se, "name")
			rack = &SnapRack{ID: id, Label: name, Cards: map[int]*SnapCard{}}
			racks = append(racks, rack)
			card = nil
		case "card":
			if rack == nil {
				continue
			}
			sv, _ := attr(se, "slot")
			slot, err := strconv.Atoi(strings.TrimSpace(sv))
			if err != nil || slot < 1 || slot > 12 {
				return nil, fmt.Errorf("bad card slot %q", sv)
			}
			tv, _ := attr(se, "type")
			card = &SnapCard{Slot: slot - 1, Type: ParseCardType(tv), Params: NewParams()}
			if v, ok := attr(se, "showInChannelView"); ok {
				card.Show = strings.EqualFold(v, "true")
			}
			if v, ok := attr(se, "commandField"); ok {
				cmd := make([]byte, len(v)/2)
				for i := range cmd {
					x, err := strconv.ParseUint(v[i*2:i*2+2], 16, 8)
					if err != nil {
						return nil, fmt.Errorf("bad commandField %q", v)
					}
					cmd[i] = byte(x)
				}
				card.Command = cmd
			}
			rack.Cards[card.Slot] = card
		case "parameter":
			if card == nil {
				continue
			}
			k, _ := attr(se, "key")
			v, _ := attr(se, "value")
			if k != "" {
				card.Params.Set(k, v)
			}
		}
	}
	if !sawRoot {
		return nil, fmt.Errorf("not a D21m snapshot file")
	}
	return racks, nil
}

func LoadSnapshotFile(path string) ([]*SnapRack, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return ReadSnapshot(f)
}
