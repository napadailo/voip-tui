//go:build !unix

package rtpengine

import "os"

// No ControlMaster outside unix.
func ownedByMe(os.FileInfo) bool { return false }
