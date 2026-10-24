// SPDX-License-Identifier: AGPL-3.0-or-later
package pluginapi

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"remote21/internal/core"
)

type client struct {
	c  net.Conn
	rd *bufio.Reader
}

func dial(t *testing.T, dir string) *client {
	b, err := os.ReadFile(filepath.Join(dir, "plugin.json"))
	if err != nil {
		t.Fatal(err)
	}
	var d discovery
	json.Unmarshal(b, &d)
	c, err := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", d.Port))
	if err != nil {
		t.Fatal(err)
	}
	fmt.Fprintf(c, `{"hello":{"token":%q,"client":"test"}}`+"\n", d.Token)
	return &client{c, bufio.NewReader(c)}
}

func (c *client) next(t *testing.T) map[string]json.RawMessage {
	t.Helper()
	c.c.SetReadDeadline(time.Now().Add(3 * time.Second))
	for {
		line, err := c.rd.ReadBytes('\n')
		if err != nil {
			t.Fatal(err)
		}
		var m map[string]json.RawMessage
		json.Unmarshal(line, &m)
		if _, ping := m["ping"]; !ping {
			return m
		}
	}
}

func TestPluginAPI(t *testing.T) {
	dir := t.TempDir()
	cfg := core.LoadConfig(filepath.Join(dir, "settings.xml"))
	cfg.Set("COM_Port", core.DemoPort)
	app := core.NewApp(cfg)
	app.Start()
	defer app.Close()
	app.Do(core.Op{Op: "settings", Value: map[string]any{"port": core.DemoPort}})
	time.Sleep(300 * time.Millisecond) // first poll
	app.Poll()
	s, err := Start(app, dir, "tok", "http://x", "test")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	c := dial(t, dir)
	if _, ok := c.next(t)["hello"]; !ok {
		t.Fatal("no hello")
	}
	var lay core.PluginLayout
	json.Unmarshal(c.next(t)["layout"], &lay)
	if !lay.Online || len(lay.Channels) < 20 || len(lay.Params) < 50 {
		t.Fatalf("layout: online=%v channels=%d params=%d", lay.Online, len(lay.Channels), len(lay.Params))
	}

	// a second plug-in instance changes a gain; the first one is told
	c2 := dial(t, dir)
	c2.next(t)
	c2.next(t)
	fmt.Fprintln(c2.c, `{"set":{"id":"s2c4","value":1}}`) // bad id -> error to c2 only
	if _, ok := c2.next(t)["error"]; !ok {
		t.Fatal("expected error")
	}
	fmt.Fprintln(c2.c, `{"set":{"id":"s2c4.gain","value":40}}`)
	for {
		m := c.next(t)
		if v, ok := m["values"]; ok {
			var vals map[string]int
			json.Unmarshal(v, &vals)
			if vals["s2c4.gain"] == 40 {
				break
			}
		}
	}
	// a label change resends the layout
	app.Do(core.Op{Op: "label", Strip: "s2c4", Text: "Overhead L"})
	for {
		m := c.next(t)
		if l, ok := m["layout"]; ok {
			json.Unmarshal(l, &lay)
			for _, ch := range lay.Channels {
				if ch.ID == "s2c4" && ch.Label == "Overhead L" {
					goto done
				}
			}
		}
	}
done:
	// track names: the app takes them on request; comments from a plug-in reach the app
	fmt.Fprintln(c2.c, `{"track":{"strip":"s2c3","name":"Snare top"}}`)
	fmt.Fprintln(c2.c, `{"comment":{"strip":"s2c3","text":"SM57, 3 cm"}}`)
	deadline := time.Now().Add(2 * time.Second)
	for app.State().Panel.DAWNames != 1 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if msg, _ := app.Do(core.Op{Op: "takeNames"}); msg != "" {
		t.Fatal(msg)
	}
	for {
		m := c.next(t)
		if l, ok := m["layout"]; ok {
			json.Unmarshal(l, &lay)
			for _, ch := range lay.Channels {
				if ch.ID == "s2c3" && ch.Label == "Snare top" && ch.Comment == "SM57, 3 cm" {
					goto done2
				}
			}
		}
	}
done2:
	c2.c.Close() // a closed plug-in no longer offers its name
	deadline = time.Now().Add(2 * time.Second)
	for app.State().Panel.DAWNames != 0 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if n := app.State().Panel.DAWNames; n != 0 {
		t.Fatalf("track names after disconnect: %d", n)
	}
	// wrong token is refused
	conn, _ := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", s.Port()))
	fmt.Fprintln(conn, `{"hello":{"token":"nope"}}`)
	line, _ := bufio.NewReader(conn).ReadString('\n')
	if line == "" || line[:9] != `{"error":` {
		t.Fatalf("token check: %q", line)
	}
}
