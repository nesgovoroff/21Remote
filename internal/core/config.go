// SPDX-License-Identifier: AGPL-3.0-or-later
package core

import (
	"encoding/xml"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Config is the settings.xml store (key/value pairs in an <appSettings> list).
type Config struct {
	mu   sync.Mutex
	Path string
	p    *Params
}

func DefaultConfigDir() string {
	d, err := os.UserConfigDir()
	if err != nil {
		d = os.TempDir()
	}
	return filepath.Join(d, "21 Remote")
}

func LoadConfig(path string) *Config {
	c := &Config{Path: path, p: NewParams()}
	f, err := os.Open(path)
	if err != nil {
		return c
	}
	defer f.Close()
	dec := xml.NewDecoder(f)
	for {
		tok, err := dec.Token()
		if err != nil {
			break
		}
		if se, ok := tok.(xml.StartElement); ok && se.Name.Local == "add" {
			var k, v string
			for _, at := range se.Attr {
				switch at.Name.Local {
				case "key":
					k = at.Value
				case "value":
					v = at.Value
				}
			}
			if k != "" {
				c.p.Set(k, v)
			}
		}
	}
	return c
}

func (c *Config) Get(k string) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	v, _ := c.p.Get(k)
	return v
}

func (c *Config) Int(k string, def int) int {
	n, err := strconv.Atoi(strings.TrimSpace(c.Get(k)))
	if err != nil {
		return def
	}
	return n
}

func (c *Config) Set(k, v string) {
	c.mu.Lock()
	c.p.Set(k, v)
	c.mu.Unlock()
}

func (c *Config) Delete(k string) {
	c.mu.Lock()
	c.p.Delete(k)
	c.mu.Unlock()
}

func xmlEsc(s string) string {
	var b strings.Builder
	xml.EscapeText(&b, []byte(s))
	return strings.ReplaceAll(b.String(), `"`, "&#34;")
}

func (c *Config) Save() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := os.MkdirAll(filepath.Dir(c.Path), 0o755); err != nil {
		return err
	}
	var b strings.Builder
	b.WriteString("<?xml version=\"1.0\" encoding=\"utf-8\"?>\n<configuration>\n  <appSettings>\n")
	b.WriteString("    <!-- This is the configuration file for 21 Remote -->\n")
	fmt.Fprintf(&b, "    <!-- %s -->\n", time.Now().Format("Monday, 2 January 2006 / 15:04:05"))
	c.p.Each(func(k, v string) {
		fmt.Fprintf(&b, "    <add key=\"%s\" value=\"%s\" />\n", xmlEsc(k), xmlEsc(v))
	})
	b.WriteString("  </appSettings>\n</configuration>\n")
	tmp := c.Path + ".tmp"
	if err := os.WriteFile(tmp, []byte(b.String()), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, c.Path)
}
