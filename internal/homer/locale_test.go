package homer

import (
	"testing"

	"github.com/napadailo/voip-tui/i18n/i18ntest"
	_ "github.com/napadailo/voip-tui/tui" // the framework's catalogs
)

var dirs = []string{".", "ui", "config", "../../cmd/homer-tui"}

func TestCatalogs(t *testing.T) {
	i18ntest.Check(t, i18ntest.Messages(t, dirs...), map[string]map[string]string{"ru": catalogRU})
}

// technical messages that stay as they are: prefixes around text that comes
// from homer-app, the network or the OS
var untranslated = map[string]bool{
	"decode search: %w": true, "decode transaction: %w": true, "decode qos: %w": true, "decode login: %w": true,
	"unexpected data payload: %s": true, "homer: login returned empty token": true, "passwordCmd: %w": true, "tokenCmd: %w": true,
}

func TestUserTextIsMarked(t *testing.T) {
	i18ntest.CheckMarked(t, untranslated, dirs...)
}
