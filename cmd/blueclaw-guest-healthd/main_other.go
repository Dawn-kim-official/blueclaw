//go:build !linux

package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Fprintln(os.Stderr, "blueclaw-guest-healthd is only available on Linux")
	os.Exit(1)
}
