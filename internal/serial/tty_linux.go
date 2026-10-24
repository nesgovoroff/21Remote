// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build linux

package serial

import (
	"path/filepath"
	"syscall"
	"unsafe"
)

func ioctl(fd int, req uint, arg unsafe.Pointer) error {
	_, _, e := syscall.Syscall(syscall.SYS_IOCTL, uintptr(fd), uintptr(req), uintptr(arg))
	if e != 0 {
		return e
	}
	return nil
}

const (
	crtscts = 0x80000000
	cbaud   = 0x100f
)

func setupTTY(fd, baud int) error {
	_ = ioctl(fd, syscall.TIOCEXCL, nil)
	var t syscall.Termios
	if err := ioctl(fd, syscall.TCGETS, unsafe.Pointer(&t)); err != nil {
		return err
	}
	t.Iflag &^= syscall.IGNBRK | syscall.BRKINT | syscall.PARMRK | syscall.ISTRIP | syscall.INLCR | syscall.IGNCR | syscall.ICRNL | syscall.IXON | syscall.IXOFF | syscall.IXANY | syscall.INPCK
	t.Oflag &^= syscall.OPOST
	t.Lflag &^= syscall.ECHO | syscall.ECHONL | syscall.ICANON | syscall.ISIG | syscall.IEXTEN
	t.Cflag &^= syscall.CSIZE | syscall.PARENB | syscall.CSTOPB | crtscts | syscall.HUPCL | cbaud
	t.Cflag |= syscall.CS8 | syscall.CREAD | syscall.CLOCAL | baudFlag(baud)
	t.Cc[syscall.VMIN] = 0
	t.Cc[syscall.VTIME] = 1
	t.Ispeed = baudFlag(baud)
	t.Ospeed = baudFlag(baud)
	if err := ioctl(fd, syscall.TCSETS, unsafe.Pointer(&t)); err != nil {
		return err
	}
	bits := int32(syscall.TIOCM_DTR | syscall.TIOCM_RTS)
	_ = ioctl(fd, syscall.TIOCMBIC, unsafe.Pointer(&bits))
	return nil
}

func baudFlag(b int) uint32 {
	switch b {
	case 9600:
		return syscall.B9600
	case 19200:
		return syscall.B19200
	case 38400:
		return syscall.B38400
	case 57600:
		return syscall.B57600
	case 230400:
		return syscall.B230400
	}
	return syscall.B115200
}

const tcflsh = 0x540B

func flushTTY(fd int) {
	syscall.Syscall(syscall.SYS_IOCTL, uintptr(fd), tcflsh, uintptr(syscall.TCIOFLUSH))
}

func listPorts() []string {
	var out []string
	for _, g := range []string{"/dev/ttyUSB*", "/dev/ttyACM*", "/dev/ttyS[0-3]"} {
		m, _ := filepath.Glob(g)
		out = append(out, m...)
	}
	return out
}
