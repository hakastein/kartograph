//go:build kartograph_test

// Package testhook marks the points of a build and a read that tests of the kartograph binary act at. Only a binary
// built with the kartograph_test tag acts: KARTOGRAPH_TEST_HOOK names a program that runs with the point as its
// argument and that kartograph waits for, and KARTOGRAPH_TEST_CRASH names a point where the process kills itself.
package testhook

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"
)

func At(point string) {
	if hook := os.Getenv("KARTOGRAPH_TEST_HOOK"); hook != "" {
		if out, err := exec.Command(hook, point).CombinedOutput(); err != nil {
			panic(fmt.Sprintf("test hook at %s: %v: %s", point, err, out))
		}
	}
	if os.Getenv("KARTOGRAPH_TEST_CRASH") == point {
		if err := syscall.Kill(os.Getpid(), syscall.SIGKILL); err != nil {
			panic(err)
		}
		select {}
	}
}
