//go:build unix

package planrun

import "golang.org/x/sys/unix"

// cliDisk is the production [DiskChecker], backed by statfs(2). x/sys/unix
// normalizes the struct across darwin and linux (Springfield's targets), so
// available-bytes is Bavail*Bsize on both.
type cliDisk struct{}

func (cliDisk) AvailableBytes(path string) (uint64, error) {
	var st unix.Statfs_t
	if err := unix.Statfs(path, &st); err != nil {
		return 0, err
	}
	return uint64(st.Bavail) * uint64(st.Bsize), nil
}
