package tui

import "github.com/napadailo/voip-tui/i18n"

// T translates interface text (see package i18n).
func T(s string) string { return i18n.T(s) }

// Tf translates a format string and fills it in.
func Tf(format string, args ...any) string { return i18n.Tf(format, args...) }

// N marks text that is stored now and translated when it is drawn (view
// titles, column headers, hints, command help), so that :lang applies to
// views that are already open.
func N(s string) string { return s }
