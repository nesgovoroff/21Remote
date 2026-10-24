// SPDX-License-Identifier: AGPL-3.0-or-later
package core

import (
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func readState(t *testing.T, dir, want string) string {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		b, _ := ioutil.ReadFile(filepath.Join(dir, "state.txt"))
		if strings.Contains(string(b), want) {
			return string(b)
		}
		time.Sleep(20 * time.Millisecond)
	}
	b, _ := ioutil.ReadFile(filepath.Join(dir, "state.txt"))
	t.Fatalf("state.txt has no %q:\n%.600s", want, b)
	return ""
}

func TestBridge(t *testing.T) {
	a, _ := newTestApp(t)
	a.Poll()
	dir := tempDir(t)
	go a.RunBridge(dir)
	time.Sleep(100 * time.Millisecond)
	if _, err := os.Stat(filepath.Join(dir, "state.txt")); err == nil {
		t.Fatal("state written without a script")
	}
	beat := filepath.Join(dir, "client.beat")
	ioutil.WriteFile(beat, []byte("1"), 0644)

	st := readState(t, dir, "ch 1 1 1 0 ")
	if !strings.Contains(st, "online 1") || !strings.Contains(st, "card 3 2 0") || !strings.Contains(st, "val 1 1 36") {
		t.Fatalf("state:\n%.800s", st)
	}
	// MIC 1/1 name in hex
	if !strings.Contains(st, hexText("MIC 1/1")) {
		t.Fatal("no channel name")
	}

	// commands from the script: gain of MIC 1/2 to +29 dB, a comment, track names
	cmds := "set 1 2 40\ncomment 1 1 " + hexText("Бочка") + "\ntracks-begin\ntrack 1 3 " + hexText("Snare") + "\ntracks-end\n"
	f, _ := os.OpenFile(filepath.Join(dir, "commands.txt"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	f.WriteString(cmds)
	f.Close()
	waitFor(t, func() bool { return micState(a, "s0c2").Gain == 29 && stripView(a, "s0c1").Comment == "Бочка" })
	waitFor(t, func() bool { return a.State().Panel.DAWNames == 1 })
	readState(t, dir, "val 1 2 40")
	readState(t, dir, hexText("Бочка"))

	// a change in the app reaches the file
	a.Do(Op{Op: "label", Strip: "s0c4", Text: "Tom"})
	readState(t, dir, hexText("Tom"))

	// the script stops: no more writes, its track names are forgotten
	old := time.Now().Add(-time.Minute)
	os.Chtimes(beat, old, old)
	waitFor(t, func() bool { return a.State().Panel.DAWNames == 0 })
}
