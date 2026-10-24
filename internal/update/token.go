// SPDX-License-Identifier: AGPL-3.0-or-later
package update

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
)

// TokenStore keeps the GitHub access token used for a private repository.
type TokenStore interface {
	Get() string
	Set(string) error
}

const keychainService = "21 Remote GitHub token"

// NewTokenStore uses the macOS Keychain, or a 0600 file elsewhere.
func NewTokenStore(dir string) TokenStore {
	if runtime.GOOS == "darwin" {
		if _, err := exec.LookPath("/usr/bin/security"); err == nil {
			return &keychain{}
		}
	}
	return &fileStore{path: filepath.Join(dir, "github-token")}
}

type keychain struct {
	mu     sync.Mutex
	cached *string
}

func (k *keychain) Get() string {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.cached != nil {
		return *k.cached
	}
	out, err := exec.Command("/usr/bin/security", "find-generic-password", "-a", "github", "-s", keychainService, "-w").Output()
	t := ""
	if err == nil {
		t = strings.TrimSpace(string(out))
	}
	k.cached = &t
	return t
}

func (k *keychain) Set(t string) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.cached = nil
	if t == "" {
		exec.Command("/usr/bin/security", "delete-generic-password", "-a", "github", "-s", keychainService).Run()
		return nil
	}
	return exec.Command("/usr/bin/security", "add-generic-password", "-U", "-a", "github", "-s", keychainService,
		"-l", "21 Remote: GitHub access for updates", "-w", t).Run()
}

type fileStore struct{ path string }

func (f *fileStore) Get() string {
	b, _ := os.ReadFile(f.path)
	return strings.TrimSpace(string(b))
}

func (f *fileStore) Set(t string) error {
	if t == "" {
		os.Remove(f.path)
		return nil
	}
	os.MkdirAll(filepath.Dir(f.path), 0o755)
	return os.WriteFile(f.path, []byte(t), 0o600)
}

func detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	cmd.Stdin, cmd.Stdout, cmd.Stderr = nil, nil, nil
}
