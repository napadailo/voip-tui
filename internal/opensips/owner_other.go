//go:build !unix

package opensips

import "os"

// No ControlMaster outside unix.
func ownedByMe(os.FileInfo) bool { return false }
