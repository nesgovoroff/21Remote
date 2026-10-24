// SPDX-License-Identifier: AGPL-3.0-or-later
// remote21-core is the engine of 21 Remote: it talks to the D21m frame over the
// serial port and serves the user interface on 127.0.0.1.
//
// Inside the macOS app bundle it is started by the native window shell with
// --embedded; run on its own it opens the UI in the default browser.
package main

import (
	"bufio"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"remote21/internal/core"
	"remote21/internal/pluginapi"
	"remote21/internal/update"
	"remote21/web"
)

var (
	version    = "1.0.0"
	updateRepo = "" // owner/name on GitHub, set by the build (-X main.updateRepo=…)
)

func main() {
	embedded := flag.Bool("embedded", false, "run under the macOS window shell (print address, exit when stdin closes)")
	addr := flag.String("addr", "127.0.0.1:0", "listen address")
	cfgDir := flag.String("config", core.DefaultConfigDir(), "settings directory")
	noBrowser := flag.Bool("no-browser", false, "do not open a browser")
	midiFDs := flag.Bool("midi-fds", false, "the window shell passes its CoreMIDI bridge on fds 3 (in) and 4 (out)")
	showVersion := flag.Bool("version", false, "print version")
	flag.Parse()
	if *showVersion {
		fmt.Println(version)
		return
	}
	setupLog(*embedded)

	cfg := core.LoadConfig(filepath.Join(*cfgDir, "settings.xml"))
	app := core.NewApp(cfg)
	if *midiFDs {
		app.AttachMIDI(os.NewFile(3, "midi-in"), os.NewFile(4, "midi-out"))
	}
	app.Start()
	go app.RunBridge(filepath.Join(*cfgDir, "bridge")) // JS plug-ins (through the DAW script)

	tokenB := make([]byte, 16)
	rand.Read(tokenB)
	token := hex.EncodeToString(tokenB)

	ln, err := net.Listen("tcp", *addr)
	if err != nil {
		log.Fatal(err)
	}
	url := fmt.Sprintf("http://%s/?token=%s", ln.Addr().String(), token)
	srv := &http.Server{Handler: handler(app, token), ReadHeaderTimeout: 10 * time.Second}
	go srv.Serve(ln)
	log.Printf("21 Remote core %s listening on %s", version, ln.Addr())
	plugins, err := pluginapi.Start(app, *cfgDir, token, url, version)
	if err != nil {
		log.Printf("plug-in API not available: %v", err)
	} else {
		log.Printf("plug-in API on 127.0.0.1:%d", plugins.Port())
	}

	shutdown := func() {
		if plugins != nil {
			plugins.Close()
		}
		app.FlushQueue()
		cfg.Save()
		app.Close()
		os.Exit(0)
	}
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP)
	go func() { <-sig; shutdown() }()

	upd := update.New(updateRepo, version, update.NewTokenStore(*cfgDir), cfg.Get("Update_Auto") != "false", func(on bool) {
		cfg.Set("Update_Auto", strconv.FormatBool(on))
		cfg.Save()
	})
	if api := os.Getenv("R21_UPDATE_API"); api != "" { // for testing against a fake GitHub
		upd.API = api
	}
	upd.OnChange = app.Notify
	upd.Quit = func() {
		if *embedded {
			// quit the window shell; closing our stdin then ends this process too
			syscall.Kill(os.Getppid(), syscall.SIGTERM)
		}
		shutdown()
	}
	app.UpdateState = func() any { return upd.View() }
	go upd.Loop(20*time.Second, 6*time.Hour)
	updateAPI(srv.Handler.(*http.ServeMux), upd, token)
	permissionAPI(srv.Handler.(*http.ServeMux), postGuard(token), func() string { return app.Port() })

	if *embedded {
		fmt.Printf("R21-URL %s\n", url)
		os.Stdout.Sync()
		if devnull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0); err == nil {
			syscall.Dup2(int(devnull.Fd()), 1)
		}
		// the shell keeps our stdin open; EOF means it is gone
		io.Copy(io.Discard, bufio.NewReader(os.Stdin))
		shutdown()
	}
	fmt.Println("21 Remote is running at", url)
	if !*noBrowser {
		openBrowser(url)
	}
	select {}
}

func setupLog(embedded bool) {
	if runtime.GOOS == "darwin" && embedded {
		if home, err := os.UserHomeDir(); err == nil {
			dir := filepath.Join(home, "Library", "Logs")
			os.MkdirAll(dir, 0o755)
			if f, err := os.OpenFile(filepath.Join(dir, "21 Remote.log"), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644); err == nil {
				log.SetOutput(f)
			}
		}
	}
}

func openBrowser(url string) {
	switch runtime.GOOS {
	case "darwin":
		exec.Command("open", url).Start()
	case "linux":
		exec.Command("xdg-open", url).Start()
	}
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	json.NewEncoder(w).Encode(v)
}

func jsonErr(w http.ResponseWriter, err error) {
	writeJSON(w, map[string]string{"error": err.Error()})
}

func handler(app *core.App, token string) http.Handler {
	mux := http.NewServeMux()
	static, _ := fs.Sub(web.FS, ".")
	fileSrv := http.StripPrefix("/static/", http.FileServer(http.FS(static)))

	authed := func(r *http.Request) bool {
		c, err := r.Cookie("r21")
		return err == nil && subtle.ConstantTimeCompare([]byte(c.Value), []byte(token)) == 1
	}
	guard := func(h http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if !authed(r) {
				http.Error(w, "forbidden", http.StatusForbidden)
				return
			}
			if r.Method == http.MethodPost {
				if o := r.Header.Get("Origin"); o != "" && !strings.HasSuffix(o, "://"+r.Host) {
					http.Error(w, "bad origin", http.StatusForbidden)
					return
				}
			}
			h(w, r)
		}
	}
	decode := func(r *http.Request, v any) error {
		return json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(v)
	}

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		if t := r.URL.Query().Get("token"); t != "" {
			if subtle.ConstantTimeCompare([]byte(t), []byte(token)) != 1 {
				http.Error(w, "forbidden", http.StatusForbidden)
				return
			}
			http.SetCookie(w, &http.Cookie{Name: "r21", Value: token, Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode})
			http.Redirect(w, r, "/", http.StatusFound)
			return
		}
		if !authed(r) {
			http.Error(w, "Open 21 Remote from the app or from the address it printed.", http.StatusForbidden)
			return
		}
		b, _ := web.FS.ReadFile("index.html")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.Write(b)
	})
	mux.Handle("/static/", guard(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-cache")
		fileSrv.ServeHTTP(w, r)
	}))
	mux.HandleFunc("/api/state", guard(func(w http.ResponseWriter, r *http.Request) { writeJSON(w, app.State()) }))
	mux.HandleFunc("/api/log", guard(func(w http.ResponseWriter, r *http.Request) { writeJSON(w, app.Log()) }))
	mux.HandleFunc("/api/midilog", guard(func(w http.ResponseWriter, r *http.Request) { writeJSON(w, app.MIDILog()) }))
	mux.HandleFunc("/api/version", guard(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]string{"version": version, "os": runtime.GOOS, "arch": runtime.GOARCH})
	}))
	mux.HandleFunc("/api/events", guard(func(w http.ResponseWriter, r *http.Request) {
		fl, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming unsupported", 500)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-store")
		ch, cancel := app.Subscribe()
		defer cancel()
		send := func() bool {
			b, _ := json.Marshal(app.State())
			if _, err := fmt.Fprintf(w, "data: %s\n\n", b); err != nil {
				return false
			}
			fl.Flush()
			return true
		}
		if !send() {
			return
		}
		ping := time.NewTicker(20 * time.Second)
		defer ping.Stop()
		for {
			select {
			case <-r.Context().Done():
				return
			case <-ch:
				time.Sleep(40 * time.Millisecond) // coalesce bursts
				select {
				case <-ch:
				default:
				}
				if !send() {
					return
				}
			case <-ping.C:
				if _, err := io.WriteString(w, ": ping\n\n"); err != nil {
					return
				}
				fl.Flush()
			}
		}
	}))
	mux.HandleFunc("/api/op", guard(func(w http.ResponseWriter, r *http.Request) {
		var op core.Op
		if err := decode(r, &op); err != nil {
			jsonErr(w, err)
			return
		}
		msg, err := app.Do(op)
		if err != nil {
			jsonErr(w, err)
			return
		}
		writeJSON(w, map[string]any{"ok": true, "message": msg})
	}))
	mux.HandleFunc("/api/snapshot/save", guard(func(w http.ResponseWriter, r *http.Request) {
		var req struct{ Path string }
		if err := decode(r, &req); err != nil || req.Path == "" {
			jsonErr(w, fmt.Errorf("no file name given"))
			return
		}
		app.FlushQueue()
		if err := app.SaveSnapshot(expand(req.Path)); err != nil {
			jsonErr(w, err)
			return
		}
		writeJSON(w, map[string]bool{"ok": true})
	}))
	mux.HandleFunc("/api/snapshot/open", guard(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Path  string
			Force bool
		}
		if err := decode(r, &req); err != nil || req.Path == "" {
			jsonErr(w, fmt.Errorf("no file name given"))
			return
		}
		mm, err := app.OpenSnapshot(expand(req.Path), req.Force)
		if err != nil {
			jsonErr(w, err)
			return
		}
		writeJSON(w, map[string]any{"ok": true, "mismatches": mm})
	}))
	mux.HandleFunc("/api/dialog", guard(func(w http.ResponseWriter, r *http.Request) {
		var req struct{ Kind string }
		decode(r, &req)
		p, prompt, err := fileDialog(req.Kind)
		if err != nil {
			jsonErr(w, err)
			return
		}
		home, _ := os.UserHomeDir()
		writeJSON(w, map[string]any{"path": p, "prompt": prompt, "suggest": filepath.Join(home, "Session.xml")})
	}))
	return mux
}

func expand(p string) string {
	p = strings.TrimSpace(p)
	if strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			p = filepath.Join(home, p[2:])
		}
	}
	return p
}

// fileDialog shows a native macOS dialog via osascript (used when the UI runs
// in a normal browser). Elsewhere it asks the UI to prompt for a path.
func fileDialog(kind string) (path string, prompt bool, err error) {
	if runtime.GOOS != "darwin" {
		return "", true, nil
	}
	var script string
	if kind == "save" {
		script = `POSIX path of (choose file name with prompt "Save Session" default name "Session.xml")`
	} else {
		script = `POSIX path of (choose file with prompt "Open Session" of type {"xml", "public.xml"})`
	}
	out, err := exec.Command("osascript", "-e", script).Output()
	if err != nil {
		return "", false, nil // cancelled
	}
	return strings.TrimSpace(string(out)), false, nil
}

// updateAPI adds the update endpoints (same cookie check as the other API calls).
func updateAPI(mux *http.ServeMux, upd *update.Manager, token string) {
	guard := postGuard(token)
	mux.HandleFunc("/api/update/check", guard(func(w http.ResponseWriter, r *http.Request) {
		upd.Check() // the result reaches the page through the state stream
		writeJSON(w, map[string]bool{"ok": true})
	}))
	mux.HandleFunc("/api/update/install", guard(func(w http.ResponseWriter, r *http.Request) {
		if err := upd.Install(); err != nil {
			jsonErr(w, err)
			return
		}
		writeJSON(w, map[string]bool{"ok": true})
	}))
	mux.HandleFunc("/api/update/token", guard(func(w http.ResponseWriter, r *http.Request) {
		var req struct{ Token string }
		json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&req)
		if err := upd.SetToken(req.Token); err != nil {
			jsonErr(w, err)
			return
		}
		upd.Check() // the result reaches the page through the state stream
		writeJSON(w, map[string]bool{"ok": true})
	}))
	mux.HandleFunc("/api/update/auto", guard(func(w http.ResponseWriter, r *http.Request) {
		var req struct{ On bool }
		json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&req)
		upd.SetAuto(req.On)
		writeJSON(w, map[string]bool{"ok": true})
	}))
}

// postGuard admits POST requests from the app's own page only (session cookie, same origin).
func postGuard(token string) func(func(http.ResponseWriter, *http.Request)) http.HandlerFunc {
	return func(h func(w http.ResponseWriter, r *http.Request)) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			c, err := r.Cookie("r21")
			if err != nil || subtle.ConstantTimeCompare([]byte(c.Value), []byte(token)) != 1 || r.Method != http.MethodPost {
				http.Error(w, "forbidden", http.StatusForbidden)
				return
			}
			if o := r.Header.Get("Origin"); o != "" && !strings.HasSuffix(o, "://"+r.Host) {
				http.Error(w, "bad origin", http.StatusForbidden)
				return
			}
			h(w, r)
		}
	}
}
