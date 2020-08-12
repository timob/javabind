// Copyright 2016 Tim O'Brien. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// +build windows

package javabind

import "golang.org/x/sys/windows"

func GetThreadId() int {
//	return 1
	return int(windows.GetCurrentThreadId())
}
