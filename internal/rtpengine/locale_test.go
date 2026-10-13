package rtpengine

import (
	"testing"

	"github.com/napadailo/voip-tui/i18n/i18ntest"
	_ "github.com/napadailo/voip-tui/tui" // the framework's catalogs
)

var dirs = []string{".", "ui", "config", "../../cmd/rtpengine-tui"}

func TestCatalogs(t *testing.T) {
	i18ntest.Check(t, i18ntest.Messages(t, dirs...), map[string]map[string]string{"ru": catalogRU})
}

// technical messages that stay as they are: prefixes around text that comes
// from ssh, rtpengine or the OS
var untranslated = map[string]bool{
	"ssh: %s": true, "ssh -W: %s": true, "listen-cli %s: %w": true, "%s: %w": true, "rtpengine %s: %s": true,
}

func TestUserTextIsMarked(t *testing.T) {
	i18ntest.CheckMarked(t, untranslated, dirs...)
}
