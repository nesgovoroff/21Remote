// SPDX-License-Identifier: AGPL-3.0-or-later
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"
)

// macOS privacy panes the Settings page can open.
var privacyPanes = map[string]string{
	"network": "Privacy_LocalNetwork",
	"files":   "Privacy_FilesAndFolders",
	"disk":    "Privacy_AllFiles",
}

func openPrivacyPane(which string) error {
	pane, ok := privacyPanes[which]
	if !ok {
		return fmt.Errorf("unknown settings pane %q", which)
	}
	if runtime.GOOS != "darwin" {
		return errors.New("only available on macOS")
	}
	return exec.Command("/usr/bin/open", "x-apple.systempreferences:com.apple.preference.security?"+pane).Run()
}

// requestLocalNetwork does what makes macOS ask for local network access: a
// multicast DNS query, and a connection to the device server if one is set.
func requestLocalNetwork(port string) string {
	if c, err := net.Dial("udp4", "224.0.0.251:5353"); err == nil {
		// a minimal mDNS query for _services._dns-sd._udp.local
		q := []byte{0, 0, 0, 0, 0, 1, 0, 0, 0, 0, 0, 0,
			9, '_', 's', 'e', 'r', 'v', 'i', 'c', 'e', 's', 7, '_', 'd', 'n', 's', '-', 's', 'd', 4, '_', 'u', 'd', 'p', 5, 'l', 'o', 'c', 'a', 'l', 0,
			0, 12, 0, 1}
		c.Write(q)
		c.Close()
	}
	host := strings.TrimPrefix(port, "tcp://")
	if host == port || host == "" {
		return "Requested. If macOS shows a dialog, choose Allow. If it does not, access is already decided: check it in System Settings."
	}
	c, err := net.DialTimeout("tcp", host, 3*time.Second)
	if err == nil {
		c.Close()
		return "The device server at " + host + " is reachable: local network access works."
	}
	if errors.Is(err, syscall.EHOSTUNREACH) || errors.Is(err, syscall.ENETUNREACH) {
		return "The device server at " + host + " cannot be reached. If you did not allow local network access, turn it on for 21 Remote in System Settings."
	}
	return "Requested. Test connection to " + host + ": " + err.Error()
}

// requestFolders reads the usual snapshot folders; macOS asks once per folder.
func requestFolders() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return err.Error()
	}
	var parts []string
	for _, name := range []string{"Desktop", "Documents", "Downloads"} {
		_, err := os.ReadDir(filepath.Join(home, name))
		switch {
		case err == nil:
			parts = append(parts, name+": allowed")
		case errors.Is(err, os.ErrPermission) || errors.Is(err, syscall.EPERM):
			parts = append(parts, name+": not allowed")
		case errors.Is(err, os.ErrNotExist):
		default:
			parts = append(parts, name+": "+err.Error())
		}
	}
	if len(parts) == 0 {
		return "No Desktop, Documents or Downloads folder found."
	}
	return strings.Join(parts, ", ") + "."
}

// permissionAPI: POST /api/system {action: "network"|"files"|"open", pane}
func permissionAPI(mux *http.ServeMux, guard func(func(http.ResponseWriter, *http.Request)) http.HandlerFunc, currentPort func() string) {
	mux.HandleFunc("/api/system", guard(func(w http.ResponseWriter, r *http.Request) {
		var req struct{ Action, Pane, URL string }
		json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&req)
		var msg string
		var err error
		switch req.Action {
		case "network":
			msg = requestLocalNetwork(currentPort())
		case "files":
			msg = requestFolders()
		case "open":
			err = openPrivacyPane(req.Pane)
		case "url": // links in the app (source code page) open in the browser
			if !strings.HasPrefix(req.URL, "https://github.com/") {
				err = errors.New("only GitHub links are opened")
			} else if runtime.GOOS == "darwin" {
				err = exec.Command("/usr/bin/open", req.URL).Run()
			}
		default:
			err = fmt.Errorf("unknown action %q", req.Action)
		}
		if err != nil {
			jsonErr(w, err)
			return
		}
		writeJSON(w, map[string]string{"message": msg})
	}))
}
