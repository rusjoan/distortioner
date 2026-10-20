//go:build unix

package metrics

import "syscall"

// diskSpace returns free and total bytes of the filesystem holding path.
func diskSpace(path string) (free, total float64, ok bool) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0, 0, false
	}
	bs := float64(st.Bsize)
	return float64(st.Bavail) * bs, float64(st.Blocks) * bs, true
}
