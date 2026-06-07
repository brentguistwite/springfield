//go:build !unix

package planrun

import "errors"

// cliDisk on non-unix platforms cannot portably measure free space, so it
// reports an error. Prepare treats that as "skip the preflight" — an
// unmeasurable platform fails open rather than blocking every run.
type cliDisk struct{}

func (cliDisk) AvailableBytes(string) (uint64, error) {
	return 0, errors.New("disk-free check unsupported on this platform")
}
