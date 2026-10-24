// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build darwin

package serial

import (
	"path/filepath"
	"syscall"
	"unsafe"
)

// libc ioctl is variadic; Go's runtime·syscall trampoline on darwin/arm64
// places the 3rd argument on the stack as Apple's ABI requires.
//
//go:linkname syscall_syscall syscall.syscall
func syscall_syscall(fn, a1, a2, a3 uintptr) (r1, r2 uintptr, err syscall.Errno)

var libc_ioctl_trampoline_addr uintptr

//go:cgo_import_dynamic libc_ioctl ioctl "/usr/lib/libSystem.B.dylib"

func ioctl(fd int, req uint, arg unsafe.Pointer) error {
	_, _, e := syscall_syscall(libc_ioctl_trampoline_addr, uintptr(fd), uintptr(req), uintptr(arg))
	if e != 0 {
		return e
	}
	return nil
}

const (
	ccts_oflow = 0x00010000
	crts_iflow = 0x00020000
)

func setupTTY(fd, baud int) error {
	// exclusive access, like Win32 CreateFile with no sharing
	_ = ioctl(fd, syscall.TIOCEXCL, nil)
	var t syscall.Termios
	if err := ioctl(fd, syscall.TIOCGETA, unsafe.Pointer(&t)); err != nil {
		return err
	}
	t.Iflag &^= syscall.IGNBRK | syscall.BRKINT | syscall.PARMRK | syscall.ISTRIP | syscall.INLCR | syscall.IGNCR | syscall.ICRNL | syscall.IXON | syscall.IXOFF | syscall.IXANY | syscall.INPCK
	t.Oflag &^= syscall.OPOST
	t.Lflag &^= syscall.ECHO | syscall.ECHONL | syscall.ICANON | syscall.ISIG | syscall.IEXTEN
	t.Cflag &^= syscall.CSIZE | syscall.PARENB | syscall.CSTOPB | ccts_oflow | crts_iflow | syscall.HUPCL
	t.Cflag |= syscall.CS8 | syscall.CREAD | syscall.CLOCAL
	t.Cc[syscall.VMIN] = 0
	t.Cc[syscall.VTIME] = 1 // 100 ms
	t.Ispeed = uint64(baud)
	t.Ospeed = uint64(baud)
	if err := ioctl(fd, syscall.TIOCSETA, unsafe.Pointer(&t)); err != nil {
		return err
	}
	bits := int32(syscall.TIOCM_DTR | syscall.TIOCM_RTS)
	_ = ioctl(fd, syscall.TIOCMBIC, unsafe.Pointer(&bits))
	return nil
}

func flushTTY(fd int) {
	what := int32(syscall.TCIOFLUSH)
	_ = ioctl(fd, syscall.TIOCFLUSH, unsafe.Pointer(&what))
}

func listPorts() []string {
	m, _ := filepath.Glob("/dev/cu.*")
	return m
}
