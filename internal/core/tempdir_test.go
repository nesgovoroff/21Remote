// SPDX-License-Identifier: AGPL-3.0-or-later
package core

import (
	"io/ioutil"
	"testing"
)

// tempDir is t.TempDir (Go 1.15).
func tempDir(t *testing.T) string {
	d, err := ioutil.TempDir("", "remote21-test-")
	if err != nil {
		t.Fatal(err)
	}
	return d
}
