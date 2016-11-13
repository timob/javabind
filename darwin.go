// Copyright 2016 Tim O'Brien. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// +build darwin

package javabind

/*
#include <sys/syscall.h>

*/
import "C"

import "syscall"

func GetThreadId() int {
	i, _, _ := syscall.Syscall(C.SYS_thread_selfid, 0, 0, 0)
        return int(i)
}
