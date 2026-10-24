// SPDX-License-Identifier: AGPL-3.0-or-later
package update

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestNewer(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{{"1.0.12", "1.0.9", true}, {"v1.0.9", "1.0.12", false}, {"1.1", "1.0.99", true}, {"1.0.0", "1.0.0", false}, {"1.0.1", "1.0", true}}
	for _, c := range cases {
		if Newer(c.a, c.b) != c.want {
			t.Errorf("Newer(%s,%s) != %v", c.a, c.b, c.want)
		}
	}
}

type memTokens struct{ t string }

func (m *memTokens) Get() string        { return m.t }
func (m *memTokens) Set(s string) error { m.t = s; return nil }

func fakeGitHub(t *testing.T, token string, zipBytes []byte) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+token {
			http.Error(w, `{"message":"Not Found"}`, 404) // GitHub hides private repos
			return
		}
		switch r.URL.Path {
		case "/repos/o/r/releases/latest":
			json.NewEncoder(w).Encode(map[string]any{
				"tag_name": "v1.0.15", "name": "21 Remote 1.0.15", "body": "Faster polling", "html_url": "https://github.com/o/r/releases/v1.0.15",
				"published_at": "2026-10-24T00:00:00Z",
				"assets":       []map[string]any{{"id": 7, "name": "21-Remote-1.0.15.pkg", "size": len(zipBytes)}},
			})
		case "/repos/o/r/releases/assets/7":
			if r.Header.Get("Accept") != "application/octet-stream" {
				http.Error(w, "accept", 400)
				return
			}
			w.Write(zipBytes)
		default:
			http.NotFound(w, r)
		}
	}))
}

func TestCheckPrivateRepo(t *testing.T) {
	srv := fakeGitHub(t, "secret", nil)
	defer srv.Close()
	tok := &memTokens{}
	changes := 0
	m := New("o/r", "1.0.9", tok, true, nil)
	m.API = srv.URL
	m.OnChange = func() { changes++ }
	v := m.Check()
	if v.Available || !strings.Contains(v.Error, "private") || !v.NeedsToken {
		t.Fatalf("without token: %+v", v)
	}
	m.SetToken("secret")
	v = m.Check()
	if !v.Available || v.Latest != "1.0.15" || v.Notes != "Faster polling" || v.Error != "" || !v.HasToken || v.NeedsToken {
		t.Fatalf("with token: %+v", v)
	}
	if changes == 0 {
		t.Fatal("no change notifications")
	}
	m.Current = "1.0.15"
	if v := m.Check(); v.Available {
		t.Fatal("same version must not be offered")
	}
}

func TestDownloadAndInstall(t *testing.T) {
	payload := append([]byte("xar!"), make([]byte, 5000)...)
	srv := fakeGitHub(t, "tk", payload)
	defer srv.Close()
	m := New("o/r", "1.0.1", &memTokens{"tk"}, true, nil)
	m.API = srv.URL
	m.Check()
	dst := filepath.Join(t.TempDir(), "d.pkg")
	a, _ := pkgAsset(m.rel)
	if err := m.download(t.Context(), a, dst); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(dst)
	if string(b) != string(payload) || !strings.Contains(m.View().Progress, "100%") {
		t.Fatalf("download: %d bytes, progress %q", len(b), m.View().Progress)
	}
	if checkPkg(dst) != nil {
		t.Fatal("package not recognised")
	}
	os.WriteFile(dst, []byte("<html>"), 0o644)
	if checkPkg(dst) == nil {
		t.Fatal("non-package accepted")
	}

	// the installer gets the downloaded package; a cancelled password dialog is reported
	var got string
	runInstaller = func(pkg, prompt string) ([]byte, error) {
		got = pkg
		return []byte("execution error: User canceled. (-128)"), fmt.Errorf("exit 1")
	}
	m.AppPath = "/Applications/21 Remote.app"
	err := m.install(m.rel)
	if err == nil || err.Error() != "Update cancelled." || !strings.HasSuffix(got, ".pkg") {
		t.Fatalf("cancel: %v (%s)", err, got)
	}
}

// RelaunchScript waits for both processes and then opens the app.
func TestRelaunchScript(t *testing.T) {
	root := t.TempDir()
	bin := filepath.Join(root, "bin")
	os.MkdirAll(bin, 0o755)
	marker := filepath.Join(root, "opened")
	os.WriteFile(filepath.Join(bin, "open"), []byte("#!/bin/sh\necho \"$@\" > "+marker+"\n"), 0o755)
	sleeper := exec.Command("sleep", "0.6")
	sleeper.Start()
	go sleeper.Wait()
	cmd := exec.Command("/bin/bash", "-c", RelaunchScript, "relaunch", "/Applications/21 Remote.app", fmt.Sprint(sleeper.Process.Pid), "0")
	cmd.Env = append(os.Environ(), "PATH="+bin+":"+os.Getenv("PATH"))
	start := time.Now()
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, out)
	}
	if time.Since(start) < 500*time.Millisecond {
		t.Fatal("did not wait for the app to quit")
	}
	b, _ := os.ReadFile(marker)
	if !strings.Contains(string(b), "/Applications/21 Remote.app") {
		t.Fatalf("not reopened: %q", b)
	}
}
