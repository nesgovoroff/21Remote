// SPDX-License-Identifier: AGPL-3.0-or-later
// Package pluginapi lets the "21 Channel" and "21 Overview" DAW plug-ins
// talk to the running app. Only the app owns the serial/network link to the
// rack; any number of plug-in instances connect here.
//
// Transport: TCP on 127.0.0.1, one JSON object per line. The port and a token
// are written to "<config dir>/plugin.json" (mode 0600) for the plug-ins.
//
//	plug-in -> app:  {"hello":{"token":"…","client":"21 Channel"}}
//	                 {"set":{"id":"s0c1.gain","value":25}}
//	                 {"track":{"strip":"s0c1","name":"Kick"}}   (DAW track name of a 21 Channel)
//	                 {"comment":{"strip":"s0c1","text":"…"}}
//	app -> plug-in:  {"hello":{"ok":true,"version":"1.0.0","url":"http://127.0.0.1:…"}}
//	                 {"layout":{rack, online, module, channels:[…], params:[…]}}   (on connect and when it changes)
//	                 {"values":{"s0c1.gain":25, …}}                               (only what changed)
//	                 {"error":"…"}
package pluginapi

import (
	"bufio"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"

	"remote21/internal/core"
)

// DefaultPort is tried first so the plug-ins usually find the app quickly.
const DefaultPort = 47210

type Backend interface {
	PluginLayout() core.PluginLayout
	ParamValues() map[string]int
	SetParam(id string, v int) error
	Subscribe() (chan struct{}, func())
	SetPluginTrack(conn int, strip, name string)
	SetComment(strip, text string) error
}

type Server struct {
	b       Backend
	token   string
	url     string
	version string
	ln      net.Listener
	file    string

	mu      sync.Mutex
	clients int
	nextID  int
}

type discovery struct {
	Port    int    `json:"port"`
	Token   string `json:"token"`
	URL     string `json:"url"`
	PID     int    `json:"pid"`
	Version string `json:"version"`
}

// Start listens and writes the discovery file into dir.
func Start(b Backend, dir, token, url, version string) (*Server, error) {
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", DefaultPort))
	if err != nil {
		ln, err = net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			return nil, err
		}
	}
	s := &Server{b: b, token: token, url: url, version: version, ln: ln, file: filepath.Join(dir, "plugin.json")}
	d, _ := json.Marshal(discovery{Port: ln.Addr().(*net.TCPAddr).Port, Token: token, URL: url, PID: os.Getpid(), Version: version})
	os.MkdirAll(dir, 0o755)
	if err := os.WriteFile(s.file, d, 0o600); err != nil {
		ln.Close()
		return nil, err
	}
	go s.acceptLoop()
	return s, nil
}

func (s *Server) Port() int { return s.ln.Addr().(*net.TCPAddr).Port }

// Close stops the server and removes the discovery file.
func (s *Server) Close() {
	s.ln.Close()
	os.Remove(s.file)
}

func (s *Server) Clients() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.clients
}

func (s *Server) acceptLoop() {
	for {
		c, err := s.ln.Accept()
		if err != nil {
			return
		}
		go s.serve(c)
	}
}

type inMsg struct {
	Hello *struct {
		Token  string `json:"token"`
		Client string `json:"client"`
	} `json:"hello"`
	Set *struct {
		ID    string  `json:"id"`
		Value float64 `json:"value"`
	} `json:"set"`
	Track *struct {
		Strip string `json:"strip"`
		Name  string `json:"name"`
	} `json:"track"`
	Comment *struct {
		Strip string `json:"strip"`
		Text  string `json:"text"`
	} `json:"comment"`
}

func (s *Server) serve(c net.Conn) {
	defer c.Close()
	if tc, ok := c.(*net.TCPConn); ok {
		tc.SetNoDelay(true)
	}
	rd := bufio.NewReaderSize(c, 64*1024)
	var wmu sync.Mutex
	send := func(v any) error {
		b, err := json.Marshal(v)
		if err != nil {
			return err
		}
		wmu.Lock()
		defer wmu.Unlock()
		c.SetWriteDeadline(time.Now().Add(3 * time.Second))
		_, err = c.Write(append(b, '\n'))
		return err
	}

	// handshake
	c.SetReadDeadline(time.Now().Add(5 * time.Second))
	line, err := rd.ReadBytes('\n')
	if err != nil {
		return
	}
	var hello inMsg
	if json.Unmarshal(line, &hello) != nil || hello.Hello == nil ||
		subtle.ConstantTimeCompare([]byte(hello.Hello.Token), []byte(s.token)) != 1 {
		send(map[string]string{"error": "bad token"})
		return
	}
	c.SetReadDeadline(time.Time{})
	s.mu.Lock()
	s.clients++
	s.nextID++
	connID := s.nextID
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.clients--
		s.mu.Unlock()
		s.b.SetPluginTrack(connID, "", "")
	}()
	log.Printf("plug-in connected: %s", hello.Hello.Client)
	if send(map[string]any{"hello": map[string]any{"ok": true, "version": s.version, "url": s.url}}) != nil {
		return
	}

	done := make(chan struct{})
	go func() { // reader
		defer close(done)
		for {
			line, err := rd.ReadBytes('\n')
			if err != nil {
				return
			}
			var m inMsg
			if json.Unmarshal(line, &m) != nil {
				continue
			}
			if m.Set != nil {
				v := int(m.Set.Value)
				if m.Set.Value < 0 {
					v = int(m.Set.Value - 0.5)
				} else {
					v = int(m.Set.Value + 0.5)
				}
				if err := s.b.SetParam(m.Set.ID, v); err != nil {
					send(map[string]string{"error": err.Error()})
				}
			}
			if m.Track != nil {
				s.b.SetPluginTrack(connID, m.Track.Strip, m.Track.Name)
			}
			if m.Comment != nil {
				if err := s.b.SetComment(m.Comment.Strip, m.Comment.Text); err != nil {
					send(map[string]string{"error": err.Error()})
				}
			}
		}
	}()

	ch, cancel := s.b.Subscribe()
	defer cancel()
	var lastLayout []byte
	last := map[string]int{}
	push := func() error {
		l := s.b.PluginLayout()
		lb, _ := json.Marshal(l)
		if lastLayout == nil || layoutChanged(l, lastLayout) {
			lastLayout = lb
			if err := send(map[string]any{"layout": l}); err != nil {
				return err
			}
			for _, p := range l.Params {
				last[p.ID] = p.Value
			}
			return nil
		}
		return nil
	}
	if push() != nil {
		return
	}
	ping := time.NewTicker(10 * time.Second)
	defer ping.Stop()
	for {
		select {
		case <-done:
			return
		case <-ping.C:
			if send(map[string]any{"ping": time.Now().Unix()}) != nil {
				return
			}
		case <-ch:
			time.Sleep(10 * time.Millisecond) // coalesce bursts
			select {
			case <-ch:
			default:
			}
			vals := s.b.ParamValues()
			diff := map[string]int{}
			for id, v := range vals {
				if o, ok := last[id]; !ok || o != v {
					diff[id] = v
					last[id] = v
				}
			}
			// structure changes (cards, labels, colours) resend the layout
			if l := s.b.PluginLayout(); layoutChanged(l, lastLayout) {
				if push() != nil {
					return
				}
				continue
			}
			if len(diff) > 0 {
				if send(map[string]any{"values": diff}) != nil {
					return
				}
			}
		}
	}
}

// layoutChanged compares everything except parameter values.
func layoutChanged(l core.PluginLayout, last []byte) bool {
	var prev core.PluginLayout
	if json.Unmarshal(last, &prev) != nil {
		return true
	}
	strip := func(x core.PluginLayout) string {
		ps := make([]core.Param, len(x.Params))
		for i, p := range x.Params {
			p.Value = 0
			ps[i] = p
		}
		x.Params = ps
		b, _ := json.Marshal(x)
		return string(b)
	}
	return strip(l) != strip(prev)
}
