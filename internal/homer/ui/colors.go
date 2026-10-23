package ui

import (
	"strings"

	"github.com/gdamore/tcell/v2"
	"github.com/napadailo/voip-tui/tui/theme"
)

// methodColor colors SIP methods and responses by class.
func methodColor(method string) tcell.Color {
	m := strings.ToUpper(strings.TrimSpace(method))
	if m == "" {
		return theme.Fg
	}
	if c := m[0]; c >= '1' && c <= '6' && len(m) >= 3 {
		switch c {
		case '1':
			return tcell.ColorSlateGray
		case '2':
			return theme.OK
		case '3':
			return tcell.ColorMediumPurple
		default:
			return theme.Err
		}
	}
	switch m {
	case "INVITE":
		return tcell.ColorAqua
	case "ACK", "PRACK":
		return tcell.ColorLightSkyBlue
	case "BYE", "CANCEL":
		return theme.Warn
	case "REGISTER":
		return tcell.ColorMediumPurple
	case "OPTIONS", "NOTIFY", "SUBSCRIBE", "INFO", "UPDATE", "MESSAGE", "REFER", "PUBLISH":
		return tcell.ColorLightSteelBlue
	}
	return theme.Fg
}
