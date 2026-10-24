// SPDX-License-Identifier: AGPL-3.0-or-later
package compat

import (
	"errors"
	"net"
	"os"
	"syscall"
	"testing"
)

type myErr struct{ n int }

func (e *myErr) Error() string { return "my" }

func TestErrors(t *testing.T) {
	sentinel := errors.New("sentinel")
	e := Wrap("context:", sentinel)
	if e.Error() != "context: sentinel" || !Is(e, sentinel) || Is(e, errors.New("sentinel")) {
		t.Fatal(e)
	}
	op := &net.OpError{Op: "dial", Err: os.NewSyscallError("connect", syscall.EHOSTUNREACH)}
	if !Is(op, syscall.EHOSTUNREACH) || Is(op, syscall.ENETUNREACH) {
		t.Fatal("net error chain")
	}
	var m *myErr
	if !As(Wrap("x", &myErr{3}), &m) || m.n != 3 {
		t.Fatal("As")
	}
	if b, a, ok := Cut("set 1 2", " "); b != "set" || a != "1 2" || !ok {
		t.Fatal("Cut")
	}
}
