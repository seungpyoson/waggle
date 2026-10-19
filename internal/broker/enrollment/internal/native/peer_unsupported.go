//go:build !darwin && !linux

package native

import "fmt"

func peerUID(uintptr) (uint32, error) {
	return 0, fmt.Errorf("native peer verification is unsupported on this operating system")
}
