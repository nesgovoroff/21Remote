// SPDX-License-Identifier: AGPL-3.0-or-later

// Package compat provides what newer Go versions have in the standard library:
// this branch is built with Go 1.12, the last Go that runs on macOS 10.10.
package compat

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"reflect"
	"strings"
)

// Unwrap returns the error wrapped by err (also for the standard error types
// that only got Unwrap methods in Go 1.13).
func Unwrap(err error) error {
	switch e := err.(type) {
	case interface{ Unwrap() error }:
		return e.Unwrap()
	case *net.OpError:
		return e.Err
	case *os.SyscallError:
		return e.Err
	case *os.PathError:
		return e.Err
	case *os.LinkError:
		return e.Err
	case *url.Error:
		return e.Err
	}
	return nil
}

// Is reports whether err or an error it wraps is target (errors.Is).
func Is(err, target error) bool {
	if target == nil {
		return err == nil
	}
	comparable := reflect.TypeOf(target).Comparable()
	for err != nil {
		if comparable && reflect.TypeOf(err).Comparable() && err == target {
			return true
		}
		if x, ok := err.(interface{ Is(error) bool }); ok && x.Is(target) {
			return true
		}
		err = Unwrap(err)
	}
	return false
}

// As finds the first error in err's chain that can be stored in *target (errors.As).
func As(err error, target interface{}) bool {
	val := reflect.ValueOf(target)
	if val.Kind() != reflect.Ptr || val.IsNil() {
		panic("compat.As: target must be a non-nil pointer")
	}
	typ := val.Type().Elem()
	for err != nil {
		if reflect.TypeOf(err).AssignableTo(typ) {
			val.Elem().Set(reflect.ValueOf(err))
			return true
		}
		if x, ok := err.(interface{ As(interface{}) bool }); ok && x.As(target) {
			return true
		}
		err = Unwrap(err)
	}
	return false
}

type wrapError struct {
	msg string
	err error
}

func (e *wrapError) Error() string { return strings.TrimSpace(e.msg + " " + e.err.Error()) }
func (e *wrapError) Unwrap() error { return e.err }

// Wrap returns an error with the text msg followed by err's text, that wraps
// err (fmt.Errorf with %w at the end).
func Wrap(msg string, err error) error { return &wrapError{msg, err} }

// Cut slices s around the first sep (strings.Cut).
func Cut(s, sep string) (before, after string, found bool) {
	if i := strings.Index(s, sep); i >= 0 {
		return s[:i], s[i+len(sep):], true
	}
	return s, "", false
}

// UserConfigDir is os.UserConfigDir.
func UserConfigDir() (string, error) {
	switch {
	case fileExists("/System/Library/CoreServices/SystemVersion.plist"): // macOS
		home := os.Getenv("HOME")
		if home == "" {
			return "", fmt.Errorf("$HOME is not defined")
		}
		return home + "/Library/Application Support", nil
	case os.Getenv("XDG_CONFIG_HOME") != "":
		return os.Getenv("XDG_CONFIG_HOME"), nil
	}
	home := os.Getenv("HOME")
	if home == "" {
		return "", fmt.Errorf("neither $XDG_CONFIG_HOME nor $HOME are defined")
	}
	return home + "/.config", nil
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}
