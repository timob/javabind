// Copyright 2016 Tim O'Brien. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// +build linux darwin

package javabind

import "syscall"

func GetThreadId() int {
	return syscall.Gettid()
}
