//go:build !linux

package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Fprintln(os.Stderr, "blueclaw-vsock-http-proxy is only available on Linux")
	os.Exit(1)
}
