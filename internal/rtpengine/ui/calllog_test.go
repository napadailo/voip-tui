package ui

import (
	"reflect"
	"testing"
	"time"
)

func TestMergeLog(t *testing.T) {
	for _, c := range []struct {
		name              string
		shown, read, want []string
		incremental       bool
		added             int
	}{
		{"first read", nil, []string{"a", "b"}, []string{"a", "b"}, false, -1},
		{"incremental repeats the last second", []string{"a", "b"}, []string{"b", "c"}, []string{"a", "b", "c"}, true, 1},
		{"full read", []string{"a", "b"}, []string{"a", "b", "c", "d"}, []string{"a", "b", "c", "d"}, false, 2},
		{"nothing new", []string{"a", "b"}, []string{"a", "b"}, []string{"a", "b"}, false, 0},
		{"incremental without the last line", []string{"a"}, []string{"x"}, []string{"a", "x"}, true, 1},
		{"full read without it: rotated", []string{"a"}, []string{"x"}, []string{"x"}, false, -1},
	} {
		got, added := mergeLog(append([]string(nil), c.shown...), c.read, c.incremental)
		if !reflect.DeepEqual(got, c.want) || added != c.added {
			t.Errorf("%s: got %q (%d), want %q (%d)", c.name, got, added, c.want, c.added)
		}
	}
}

func TestLogLineTime(t *testing.T) {
	tm, ok := logLineTime("2026-10-09T14:05:24.123456+0600 host rtpengine[1]: INFO: [x]: y")
	want := time.Date(2026, 10, 9, 8, 5, 24, 123456000, time.UTC)
	if !ok || !tm.Equal(want) {
		t.Fatalf("got %v %v, want %v", tm, ok, want)
	}
	if _, ok := logLineTime("Oct 09 14:05:24 host rtpengine[1]: x"); ok {
		t.Fatal("a line without an ISO time has no time")
	}
}
