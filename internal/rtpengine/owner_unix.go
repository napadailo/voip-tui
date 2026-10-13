//go:build unix

package rtpengine

import (
	"os"
	"syscall"
)

func ownedByMe(st os.FileInfo) bool {
	s, ok := st.Sys().(*syscall.Stat_t)
	return ok && int(s.Uid) == os.Getuid()
}
