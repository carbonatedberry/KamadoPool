//go:build unix

package logmon

import (
	"os"
	"syscall"
)

// inodeOf returns the inode number for a FileInfo on unix systems, or 0
// if the underlying stat is unavailable. Used to detect log rotation.
func inodeOf(fi os.FileInfo) uint64 {
	if st, ok := fi.Sys().(*syscall.Stat_t); ok {
		return st.Ino
	}
	return 0
}
