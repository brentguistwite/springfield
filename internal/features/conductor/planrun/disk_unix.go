//go:build unix

package planrun

import (
	"fmt"

	"golang.org/x/sys/unix"
)

// cliDisk is the production [DiskChecker], backed by statfs(2). x/sys/unix
// normalizes the struct across darwin and linux (Springfield's targets), so
// available-bytes is Bavail*Bsize on both.
type cliDisk struct{}

func (cliDisk) AvailableBytes(path string) (uint64, error) {
	var st unix.Statfs_t
	if err := unix.Statfs(path, &st); err != nil {
		return 0, err
	}
	// Bsize is signed int64 on linux; a non-positive value (buggy FUSE/driver)
	// would wrap to a huge uint64 and make the preflight pass a full disk. Treat
	// it as unmeasurable so the check fails open rather than silently wrong.
	if st.Bsize <= 0 {
		return 0, fmt.Errorf("statfs returned non-positive block size %d for %s", st.Bsize, path)
	}
	return uint64(st.Bavail) * uint64(st.Bsize), nil
}
