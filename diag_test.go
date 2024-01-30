// Copyright (c) 2022
// Author: Jeff Weisberg <tcp4me.com!jaw>
// Created: 2022-Sep-05 22:25 (EDT)
// Function: test

package diag_test

import (
	"testing"

	"github.com/jaw0/acdiag"
)

func TestDiag(t *testing.T) {
	d := diag.Logger("test")
	d.Verbose("test")
}
