//go:build !windows

package cmd

import (
	"os"
	"os/signal"
	"runtime"
	"syscall"
)

// Fylvo: SIGUSR1 writes the stacks of all goroutines to the standard error output, and the
// server keeps running (docker kill -s USR1 <container>; the stacks are in docker logs).
func watchStackDump() {
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGUSR1)
	go func() {
		for range signals {
			buf := make([]byte, 1<<20)
			for {
				n := runtime.Stack(buf, true)
				if n < len(buf) || len(buf) >= 64<<20 {
					buf = buf[:n]
					break
				}
				buf = make([]byte, 2*len(buf))
			}
			os.Stderr.WriteString("=== goroutine dump (SIGUSR1) ===\n")
			os.Stderr.Write(buf)
			os.Stderr.WriteString("=== end of goroutine dump ===\n")
		}
	}()
}
