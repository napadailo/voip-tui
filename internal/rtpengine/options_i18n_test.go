package rtpengine

import (
	"fmt"
	"sort"
	"testing"

	"github.com/napadailo/voip-tui/i18n"
)

// TestOptionTranslations fails when an option has no translated description or
// the English text changed since it was translated (after make gen).
func TestOptionTranslations(t *testing.T) {
	type table struct {
		name string
		en   map[string]optionSpec
		ru   map[string]optionTr
	}
	var tables []table
	for lang, byDaemon := range optionTranslations {
		tables = append(tables, table{lang + " rtpengine", daemonOptions, byDaemon[DaemonRTPEngine]}, table{lang + " recording", recordingOptions, byDaemon[DaemonRecording]})
	}
	for _, tc := range tables {
		var problems []string
		for name, spec := range tc.en {
			tr, ok := tc.ru[name]
			switch {
			case !ok:
				problems = append(problems, fmt.Sprintf("%q: no translation of %q", name, spec.Desc))
			case tr.En != spec.Desc:
				problems = append(problems, fmt.Sprintf("%q: translated from %q, now %q", name, tr.En, spec.Desc))
			}
		}
		for name := range tc.ru {
			if _, ok := tc.en[name]; !ok {
				problems = append(problems, fmt.Sprintf("%q: no such option any more", name))
			}
		}
		sort.Strings(problems)
		for _, p := range problems {
			t.Errorf("%s %s", tc.name, p)
		}
	}
}

func TestOptionDescLanguage(t *testing.T) {
	defer i18n.Set("en")
	if d := optionDesc(DaemonRTPEngine, "timeout", "RTP timeout"); d != "RTP timeout" {
		t.Fatalf("en: %q", d)
	}
	_ = i18n.Set("ru")
	if d := optionDesc(DaemonRTPEngine, "timeout", "RTP timeout"); d != "Таймаут RTP" {
		t.Fatalf("ru: %q", d)
	}
	// the English text changed after make gen: the old translation is not shown
	if d := optionDesc(DaemonRTPEngine, "timeout", "RTP timeout in seconds"); d != "RTP timeout in seconds" {
		t.Fatalf("stale translation shown: %q", d)
	}
	if d := optionDesc(DaemonRecording, "table", recordingOptions["table"].Desc); d != "Таблица ядра, которую использует rtpengine" {
		t.Fatalf("recording: %q", d)
	}
}
