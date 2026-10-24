// SPDX-License-Identifier: AGPL-3.0-or-later
// Package update checks GitHub releases for a newer version of 21 Remote and,
// only when the user asks for it, installs it (app and plug-ins) and restarts the app.
package update

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

// errNeedsToken marks errors that a GitHub access token would solve.
var errNeedsToken = tokenError{}

type tokenError struct{}

func (tokenError) Error() string { return "" }

// Newer reports whether version a is newer than b ("1.0.12" > "1.0.9").
func Newer(a, b string) bool {
	pa, pb := parts(a), parts(b)
	for i := 0; i < len(pa) || i < len(pb); i++ {
		var x, y int
		if i < len(pa) {
			x = pa[i]
		}
		if i < len(pb) {
			y = pb[i]
		}
		if x != y {
			return x > y
		}
	}
	return false
}

func parts(v string) []int {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	var out []int
	for _, p := range strings.FieldsFunc(v, func(r rune) bool { return r == '.' || r == '-' || r == '+' }) {
		n, err := strconv.Atoi(p)
		if err != nil {
			break
		}
		out = append(out, n)
	}
	return out
}

type asset struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
	Size int64  `json:"size"`
}

type release struct {
	Tag       string  `json:"tag_name"`
	Name      string  `json:"name"`
	Body      string  `json:"body"`
	HTMLURL   string  `json:"html_url"`
	Published string  `json:"published_at"`
	Assets    []asset `json:"assets"`
}

// View is what the UI shows.
type View struct {
	Current    string `json:"current"`
	Latest     string `json:"latest"`
	Available  bool   `json:"available"`
	Notes      string `json:"notes"`
	URL        string `json:"url"`
	Published  string `json:"published"`
	Checked    string `json:"checked"`
	Checking   bool   `json:"checking"`
	Installing bool   `json:"installing"`
	Progress   string `json:"progress"`
	Error      string `json:"error"`
	NeedsToken bool   `json:"needsToken"` // GitHub wants a (valid) access token: the repository is private
	HasToken   bool   `json:"hasToken"`
	Auto       bool   `json:"auto"`
	CanInstall bool   `json:"canInstall"`
	Repo       string `json:"repo"`
}

// Manager keeps the update state. OnChange is called after every state change.
type Manager struct {
	Repo     string // owner/name
	Current  string
	API      string // https://api.github.com (overridable in tests)
	Tokens   TokenStore
	OnChange func()
	// AppPath is the .app bundle being run ("" if not running from a bundle).
	AppPath string
	// Quit is called after the installer has been started; it must make the app exit.
	Quit func()

	mu      sync.Mutex
	v       View
	rel     *release
	auto    bool
	setAuto func(bool)
	client  *http.Client
}

func New(repo, current string, tokens TokenStore, auto bool, setAuto func(bool)) *Manager {
	m := &Manager{Repo: repo, Current: current, API: "https://api.github.com", Tokens: tokens, auto: auto, setAuto: setAuto,
		client: &http.Client{Timeout: 10 * time.Minute}}
	if exe, err := os.Executable(); err == nil {
		// …/21 Remote.app/Contents/MacOS/remote21-core
		if b := filepath.Dir(filepath.Dir(filepath.Dir(exe))); strings.HasSuffix(b, ".app") {
			m.AppPath = b
		}
	}
	return m
}

func (m *Manager) View() View {
	m.mu.Lock()
	defer m.mu.Unlock()
	v := m.v
	v.Current = m.Current
	v.Repo = m.Repo
	v.Auto = m.auto
	v.HasToken = m.Tokens != nil && m.Tokens.Get() != ""
	v.CanInstall = m.AppPath != "" && runtime.GOOS == "darwin" && v.Available
	return v
}

func (m *Manager) update(f func(v *View)) {
	m.mu.Lock()
	f(&m.v)
	m.mu.Unlock()
	if m.OnChange != nil {
		m.OnChange()
	}
}

func (m *Manager) SetAuto(on bool) {
	m.mu.Lock()
	m.auto = on
	m.mu.Unlock()
	if m.setAuto != nil {
		m.setAuto(on)
	}
	if m.OnChange != nil {
		m.OnChange()
	}
}

func (m *Manager) SetToken(t string) error {
	if m.Tokens == nil {
		return errors.New("no token store")
	}
	if err := m.Tokens.Set(strings.TrimSpace(t)); err != nil {
		return err
	}
	m.update(func(v *View) { v.Error = "" })
	return nil
}

// Loop checks shortly after start and then every interval while automatic checks are on.
func (m *Manager) Loop(first, every time.Duration) {
	time.Sleep(first)
	for {
		m.mu.Lock()
		auto := m.auto
		m.mu.Unlock()
		if auto {
			m.Check()
		}
		time.Sleep(every)
	}
}

func (m *Manager) request(ctx context.Context, url, accept string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", accept)
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", "21-Remote/"+m.Current)
	if m.Tokens != nil {
		if t := m.Tokens.Get(); t != "" {
			req.Header.Set("Authorization", "Bearer "+t)
		}
	}
	return m.client.Do(req)
}

// Check asks GitHub for the latest release.
func (m *Manager) Check() View {
	m.update(func(v *View) { v.Checking, v.Error = true, "" })
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	rel, err := m.fetchLatest(ctx)
	m.update(func(v *View) {
		v.Checking = false
		v.Checked = time.Now().Format("2 Jan 15:04")
		v.NeedsToken = errors.Is(err, errNeedsToken)
		if err != nil {
			v.Error = strings.TrimSpace(err.Error())
			return
		}
		m.rel = rel
		v.Latest = strings.TrimPrefix(rel.Tag, "v")
		v.Available = Newer(v.Latest, m.Current)
		v.Notes = rel.Body
		v.URL = rel.HTMLURL
		if t, err := time.Parse(time.RFC3339, rel.Published); err == nil {
			v.Published = t.Local().Format("2 Jan 2006 15:04")
		}
	})
	return m.View()
}

func (m *Manager) fetchLatest(ctx context.Context) (*release, error) {
	if m.Repo == "" {
		return nil, errors.New("This build has no update source.")
	}
	resp, err := m.request(ctx, fmt.Sprintf("%s/repos/%s/releases/latest", m.API, m.Repo), "application/vnd.github+json")
	if err != nil {
		return nil, fmt.Errorf("GitHub is not reachable: %v", err)
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case 200:
	case 401:
		return nil, fmt.Errorf("GitHub refused the access token. Check or replace it below. %w", errNeedsToken)
	case 403:
		return nil, errors.New("GitHub refused the request (rate limit or token without access to the repository).")
	case 404:
		if m.Tokens == nil || m.Tokens.Get() == "" {
			return nil, fmt.Errorf("The repository is private: add a GitHub access token below to check for updates. %w", errNeedsToken)
		}
		return nil, fmt.Errorf("No release found, or the token has no access to %s. %w", m.Repo, errNeedsToken)
	default:
		return nil, fmt.Errorf("GitHub answered %s", resp.Status)
	}
	var r release
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&r); err != nil {
		return nil, fmt.Errorf("unexpected answer from GitHub: %v", err)
	}
	return &r, nil
}

func (m *Manager) download(ctx context.Context, a asset, dst string) error {
	resp, err := m.request(ctx, fmt.Sprintf("%s/repos/%s/releases/assets/%d", m.API, m.Repo, a.ID), "application/octet-stream")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("download of %s failed: %s", a.Name, resp.Status)
	}
	f, err := os.Create(dst)
	if err != nil {
		return err
	}
	pr := &progressReader{r: resp.Body, total: a.Size, report: func(s string) { m.update(func(v *View) { v.Progress = a.Name + ": " + s }) }}
	if _, err := io.Copy(f, pr); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

type progressReader struct {
	r      io.Reader
	n      int64
	total  int64
	last   time.Time
	report func(string)
}

func (p *progressReader) Read(b []byte) (int, error) {
	n, err := p.r.Read(b)
	p.n += int64(n)
	if time.Since(p.last) > 300*time.Millisecond || err == io.EOF {
		p.last = time.Now()
		if p.total > 0 {
			p.report(fmt.Sprintf("%d%%", p.n*100/p.total))
		} else {
			p.report(fmt.Sprintf("%.1f MB", float64(p.n)/1e6))
		}
	}
	return n, err
}

// Install downloads the installer package of the latest release, installs it
// (macOS asks for an administrator password: the app and the plug-ins live in
// system folders) and restarts the app.
func (m *Manager) Install() error {
	m.mu.Lock()
	rel := m.rel
	busy := m.v.Installing
	m.mu.Unlock()
	if busy {
		return errors.New("an update is already being installed")
	}
	if rel == nil || !Newer(strings.TrimPrefix(rel.Tag, "v"), m.Current) {
		return errors.New("no newer version available; check for updates first")
	}
	if m.AppPath == "" || runtime.GOOS != "darwin" {
		return errors.New("updating works only in the installed macOS app; download the installer from GitHub instead")
	}
	m.update(func(v *View) { v.Installing, v.Error, v.Progress = true, "", "Downloading…" })
	err := m.install(rel)
	if err != nil {
		m.update(func(v *View) { v.Installing, v.Error, v.Progress = false, err.Error(), "" })
		return err
	}
	m.update(func(v *View) { v.Progress = "Installed. Restarting…" })
	if m.Quit != nil {
		go func() {
			time.Sleep(700 * time.Millisecond) // let the UI show "Restarting…"
			m.Quit()
		}()
	}
	return nil
}

func findAsset(r *release, name string) (asset, bool) {
	for _, a := range r.Assets {
		if a.Name == name {
			return a, true
		}
	}
	return asset{}, false
}

// PkgAsset finds the installer package of a release.
func pkgAsset(r *release) (asset, bool) {
	for _, a := range r.Assets {
		if strings.HasSuffix(strings.ToLower(a.Name), ".pkg") {
			return a, true
		}
	}
	return asset{}, false
}

// InstalledApp is where the installer package puts the app.
const InstalledApp = "/Applications/21 Remote.app"

// runInstaller is replaced in tests.
var runInstaller = func(pkg, prompt string) ([]byte, error) {
	return exec.Command("/usr/bin/osascript",
		"-e", "on run argv",
		"-e", `do shell script "/usr/sbin/installer -pkg " & quoted form of (item 1 of argv) & " -target /" with administrator privileges with prompt (item 2 of argv)`,
		"-e", "end run", pkg, prompt).CombinedOutput()
}

func (m *Manager) install(rel *release) error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	a, ok := pkgAsset(rel)
	if !ok {
		return errors.New("the release has no installer package")
	}
	work, err := os.MkdirTemp("", "remote21-update-")
	if err != nil {
		return err
	}
	pkg := filepath.Join(work, a.Name)
	if err := m.download(ctx, a, pkg); err != nil {
		return err
	}
	if err := checkPkg(pkg); err != nil {
		return err
	}
	ver := strings.TrimPrefix(rel.Tag, "v")
	m.update(func(v *View) { v.Progress = "Installing: enter an administrator password…" })
	out, err := runInstaller(pkg, "21 Remote wants to install version "+ver+" (app and plug-ins).")
	if err != nil {
		txt := strings.TrimSpace(string(out))
		if strings.Contains(txt, "-128") || strings.Contains(strings.ToLower(txt), "cancel") {
			return errors.New("Update cancelled.")
		}
		return fmt.Errorf("the installer failed: %s", txt)
	}
	os.RemoveAll(work)

	// start the new app once this one has quit
	app := InstalledApp
	if _, err := os.Stat(app); err != nil {
		app = m.AppPath
	}
	cmd := exec.Command("/bin/bash", "-c", RelaunchScript, "relaunch", app, strconv.Itoa(os.Getppid()), strconv.Itoa(os.Getpid()))
	detach(cmd)
	if err := cmd.Start(); err != nil {
		return err
	}
	cmd.Process.Release()
	log.Printf("update: %s installed, restarting", rel.Tag)
	return nil
}

// checkPkg makes sure the download is an installer package (a xar archive).
func checkPkg(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	head := make([]byte, 4)
	if _, err := io.ReadFull(f, head); err != nil || string(head) != "xar!" {
		return errors.New("the download is not an installer package")
	}
	return nil
}

// RelaunchScript waits for the app (window shell and engine) to quit, then opens it again.
const RelaunchScript = `APP="$1"; SHELLPID="$2"; COREPID="$3"
for i in $(seq 1 150); do
  alive=0
  for p in $SHELLPID $COREPID; do [ "$p" -gt 1 ] && kill -0 "$p" 2>/dev/null && alive=1; done
  [ $alive = 0 ] && break
  sleep 0.2
done
sleep 0.5
open "$APP"
`
