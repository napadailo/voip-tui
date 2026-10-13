package tui

import (
	"testing"

	"github.com/napadailo/voip-tui/i18n/i18ntest"
)

func TestCatalogs(t *testing.T) {
	i18ntest.Check(t, i18ntest.Messages(t, ".", "theme"), map[string]map[string]string{"ru": catalogRU})
}

func TestUserTextIsMarked(t *testing.T) {
	i18ntest.CheckMarked(t, nil, ".", "theme")
}
