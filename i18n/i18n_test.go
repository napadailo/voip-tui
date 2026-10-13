package i18n

import "testing"

func TestSetAndFallback(t *testing.T) {
	Register("ru", map[string]string{"hello": "привет"})
	defer Set("en")
	if err := Set("ru_RU.UTF-8"); err != nil || Lang() != "ru" {
		t.Fatalf("Set: %v %s", err, Lang())
	}
	if T("no such message") != "no such message" {
		t.Fatal("untranslated text must stay English")
	}
	if Set("xx") == nil {
		t.Fatal("unknown language accepted")
	}
	t.Setenv("LC_ALL", "")
	t.Setenv("LC_MESSAGES", "")
	t.Setenv("LANG", "ru_RU.UTF-8")
	if FromEnv() != "ru" {
		t.Fatal("LANG not detected")
	}
	t.Setenv("LANG", "de_DE.UTF-8")
	if FromEnv() != "" {
		t.Fatal("unsupported LANG should give no language")
	}
}
