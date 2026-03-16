//go:build !windows

package main

import "syscall"

func execve(path string, args []string, env []string) error {
	return syscall.Exec(path, args, env)
}
