package opensips

import (
	"os"
	"strings"
	"testing"
)

func TestControlDirFitsSocketLimit(t *testing.T) {
	dir := ControlDir()
	if dir == "" {
		t.Fatal("no control dir on this machine")
	}
	// ssh binds "<dir>/<40 char %C>.<16 random chars>" before renaming it
	if n := len(dir + "/" + strings.Repeat("x", 40) + "." + strings.Repeat("y", 16)); n >= 104 {
		t.Fatalf("socket path would be %d bytes: %s", n, dir)
	}
	if controlPathFits("/Users/someone/Library/Caches/opensips-tui/ssh") {
		t.Fatal("the old cache path must not pass the length check")
	}
	if st, err := os.Lstat(dir); err != nil || st.Mode().Perm() != 0o700 {
		t.Fatalf("control dir %s: %v %v", dir, st.Mode(), err)
	}
}
