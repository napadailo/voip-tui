package rtpengine

import (
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestControlDirFitsSocketLimit(t *testing.T) {
	dir := controlDir()
	if dir == "" {
		t.Fatal("no control dir on this machine")
	}
	// ssh binds "<dir>/<40 char %C>.<16 random chars>" before renaming it
	if n := len(dir + "/" + strings.Repeat("x", 40) + "." + strings.Repeat("y", 16)); n >= 104 {
		t.Fatalf("socket path would be %d bytes: %s", n, dir)
	}
	if controlPathFits("/Users/someone/Library/Caches/rtpengine-tui/cm") {
		t.Fatal("the old cache path must not pass the length check")
	}
	if st, err := os.Lstat(dir); err != nil || st.Mode().Perm() != 0o700 {
		t.Fatalf("control dir %s: %v %v", dir, st.Mode(), err)
	}
}

// A tunnel process that keeps running after its stdin closes (OpenBSD nc
// does) must not hang Close.
func TestPipeCloseKillsStuckProcess(t *testing.T) {
	p, err := startPipe(exec.Command("sh", "-c", "trap '' HUP; sleep 30"))
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	p.Close()
	if d := time.Since(start); d > 2*pipeCloseGrace+time.Second { // the grace, then WaitDelay for the pipes
		t.Fatalf("Close took %v", d)
	}
}
