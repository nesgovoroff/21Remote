// SPDX-License-Identifier: AGPL-3.0-or-later
package core

import (
	"bufio"
	"bytes"
	"encoding/hex"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// The bridge serves the JS plug-ins. A JS plug-in cannot open a connection
// to another program, so a small script in the DAW ("21 Remote Bridge.lua") passes
// data between the plug-ins (shared memory of the DAW) and this app (files in
// <config dir>/bridge):
//
//	client.beat    written by the script about once a second while it runs
//	state.txt      written here (atomically) while the script runs:
//	                 R21 1
//	                 seq <n>
//	                 online <0|1>
//	                 rack <hex>
//	                 card <slot 1-12> <type> <flags>
//	                 ch <slot> <ch> <kind 1 Mic|2 DL5> <linked> <colour> <name> <label> <comment> <ids>   (texts in hex, "-" = empty)
//	                 val <slot> <cc> <value>   (every control, values as on the MIDI port)
//	commands.txt   appended by the script, read here:
//	                 set <slot> <cc> <value>
//	                 comment <slot> <ch> <hex>
//	                 tracks-begin / track <slot> <ch> <hex name> / tracks-end
//
// Card types: 0 none, 1 Mic, 2 DL5, 3 GPIO, 4 AES/EBU, 5 ADAT, 6 other; flags
// (AES/EBU): 1 SFC inputs, 2 outputs.

const bridgeTrackBase = 1 << 30 // ids of DAW tracks reported by the bridge

func hexText(s string) string {
	if s == "" {
		return "-"
	}
	return hex.EncodeToString([]byte(s))
}

func unhexText(s string) (string, bool) {
	if s == "-" {
		return "", true
	}
	b, err := hex.DecodeString(s)
	return string(b), err == nil
}

func cardCode(t int) int {
	kind, _ := stripLayout(t)
	switch {
	case t == NoCard:
		return 0
	case kind == KindMic:
		return 1
	case kind == KindDL5:
		return 2
	case kind == KindGPIO:
		return 3
	case kind == KindAES:
		return 4
	case kind == KindADAT:
		return 5
	}
	return 6
}

// bridgeStateLocked renders state.txt (without the seq line).
func (a *App) bridgeStateLocked() string {
	var b strings.Builder
	online := a.link != nil && a.rack.Online && a.rack.Module != 0xFFFF && a.rack.Module != 0
	fmt.Fprintf(&b, "online %d\nrack %s\n", b2i(online), hexText(a.rack.Label))
	for i, c := range a.rack.Cards {
		flags := 0
		if cardCode(c.Type) == 4 {
			st := aesDecode(c)
			flags = b2i(st.HasSFCIn) | b2i(st.HasOut)<<1
		}
		fmt.Fprintf(&b, "card %d %d %d\n", i+1, cardCode(c.Type), flags)
	}
	for _, s := range a.strips {
		if s.Kind != KindMic && s.Kind != KindDL5 {
			continue
		}
		kind := 1
		if s.Kind == KindDL5 {
			kind = 2
		}
		col, _ := strconv.ParseInt(strings.TrimPrefix(s.Color(), "#"), 16, 32)
		label, _ := s.param("Label")
		comment, _ := s.param("Comment")
		fmt.Fprintf(&b, "ch %d %d %d %d %d %s %s %s %s\n", s.Card.Slot+1, s.Ch, kind, b2i(s.LinkedBy != nil), col,
			hexText(s.FixedLabel()), hexText(label), hexText(comment), hexText(s.ChannelIDs()))
	}
	for _, p := range a.paramsLocked() {
		if ch, cc, ok := translatorCC(p); ok {
			fmt.Fprintf(&b, "val %d %d %d\n", ch+1, cc, translatorValue(p))
		}
	}
	return b.String()
}

// bridgeSet applies "set <slot> <cc> <value>" like the MIDI port does.
func (a *App) bridgeSet(slot, cc, v int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, p := range a.paramsLocked() {
		if ch, c, ok := translatorCC(p); ok && ch == slot-1 && c == cc && !p.ReadOnly {
			if nv := translatorParse(p, v); nv != p.Value {
				if a.setParamLocked(p.ID, nv) == nil {
					a.changed()
				}
			}
			return
		}
	}
}

// SetBridgeTracks replaces the track names reported through the bridge.
func (a *App) SetBridgeTracks(t map[string]string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for k := range a.tracks {
		if k >= bridgeTrackBase {
			delete(a.tracks, k)
		}
	}
	i := bridgeTrackBase
	for strip, name := range t {
		a.tracks[i] = trackInfo{strip, strings.TrimSpace(name)}
		i++
	}
	a.changed()
}

func (a *App) bridgeLine(line string, tracks *map[string]string) {
	f := strings.Fields(line)
	if len(f) == 0 {
		return
	}
	num := func(i int) int {
		if i >= len(f) {
			return -1
		}
		n, err := strconv.Atoi(f[i])
		if err != nil {
			return -1
		}
		return n
	}
	switch f[0] {
	case "set":
		if slot, cc, v := num(1), num(2), num(3); slot >= 1 && slot <= 12 && cc >= 0 && v >= 0 {
			a.bridgeSet(slot, cc, v)
		}
	case "comment":
		if len(f) == 4 {
			if text, ok := unhexText(f[3]); ok {
				a.SetComment(fmt.Sprintf("s%dc%d", num(1)-1, num(2)), text)
			}
		}
	case "tracks-begin":
		*tracks = map[string]string{}
	case "track":
		if *tracks != nil && len(f) == 4 {
			if name, ok := unhexText(f[3]); ok {
				(*tracks)[fmt.Sprintf("s%dc%d", num(1)-1, num(2))] = name
			}
		}
	case "tracks-end":
		if *tracks != nil {
			a.SetBridgeTracks(*tracks)
			*tracks = nil
		}
	}
}

// RunBridge serves the bridge directory until the process ends.
func (a *App) RunBridge(dir string) {
	os.MkdirAll(dir, 0755)
	cmdPath := filepath.Join(dir, "commands.txt")
	var off int64
	var lastVer uint64
	var lastWrite time.Time
	var lastState string
	seq := 0
	var tracks map[string]string
	wasAlive := false
	for range time.Tick(25 * time.Millisecond) {
		st, err := os.Stat(filepath.Join(dir, "client.beat"))
		alive := err == nil && time.Since(st.ModTime()) < 5*time.Second
		if !alive {
			if wasAlive {
				a.SetBridgeTracks(nil) // the DAW is gone
			}
			wasAlive = false
			continue
		}
		if !wasAlive {
			// a (new) script: start from the end of the command file
			if fi, err := os.Stat(cmdPath); err == nil {
				off = fi.Size()
			}
			lastVer = 0
			wasAlive = true
		}
		// commands
		if f, err := os.Open(cmdPath); err == nil {
			if fi, err := f.Stat(); err == nil {
				size := fi.Size()
				if size < off {
					off = 0 // truncated
				}
				if size > off {
					f.Seek(off, io.SeekStart)
					data, _ := ioutil.ReadAll(io.LimitReader(f, size-off))
					if n := bytes.LastIndexByte(data, '\n'); n >= 0 {
						sc := bufio.NewScanner(bytes.NewReader(data[:n+1]))
						sc.Buffer(make([]byte, 64*1024), 1<<20)
						for sc.Scan() {
							a.bridgeLine(sc.Text(), &tracks)
						}
						off += int64(n + 1)
					}
				}
				if off == size && size > 256*1024 {
					os.Truncate(cmdPath, 0)
					off = 0
				}
			}
			f.Close()
		}
		// state: on every change, and at least once a second (the script
		// sees from that the app is running)
		a.mu.Lock()
		ver := a.ver
		var state string
		if ver != lastVer || time.Since(lastWrite) > time.Second {
			state = a.bridgeStateLocked()
		}
		a.mu.Unlock()
		if state == "" {
			continue
		}
		lastVer = ver
		if state == lastState && time.Since(lastWrite) < time.Second {
			continue
		}
		seq++
		tmp := filepath.Join(dir, ".state.tmp")
		if err := ioutil.WriteFile(tmp, []byte("R21 1\nseq "+strconv.Itoa(seq)+"\n"+state), 0644); err == nil {
			if err := os.Rename(tmp, filepath.Join(dir, "state.txt")); err != nil {
				log.Printf("bridge: %v", err)
			}
		}
		lastState, lastWrite = state, time.Now()
	}
}
