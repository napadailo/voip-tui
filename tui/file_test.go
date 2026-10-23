package tui

import (
	"os"
	"path/filepath"
	"testing"
)

// What is saved is a new file only its owner reads: a file or a link
// already there is left as it is, the name gets -1.
func TestWriteNew(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "call.pcap")
	got, err := WriteNew(path, []byte("one"))
	if err != nil || got != path {
		t.Fatalf("%q %v", got, err)
	}
	if st, _ := os.Stat(got); st.Mode().Perm() != 0o600 {
		t.Fatalf("mode %o", st.Mode().Perm())
	}
	got, err = WriteNew(path, []byte("two"))
	if err != nil || got != filepath.Join(dir, "call-1.pcap") {
		t.Fatalf("%q %v", got, err)
	}
	if b, _ := os.ReadFile(path); string(b) != "one" {
		t.Fatalf("overwritten: %q", b)
	}
	target := filepath.Join(dir, "target")
	os.WriteFile(target, []byte("keep"), 0o600)
	link := filepath.Join(dir, "link.txt")
	if err := os.Symlink(target, link); err != nil {
		t.Skip(err)
	}
	if got, err := WriteNew(link, []byte("x")); err != nil || got == link {
		t.Fatalf("followed the link: %q %v", got, err)
	}
	if b, _ := os.ReadFile(target); string(b) != "keep" {
		t.Fatalf("the link's target was written: %q", b)
	}
}
