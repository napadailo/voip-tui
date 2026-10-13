// Package i18n translates the interface of the voip-tui tools. Messages are
// written in English in the code and looked up by that text, gettext style:
// whatever has no translation is shown in English. Each package registers
// the catalog of its own messages (the tui framework, every tool) with
// Register. Names that come from the managed software (CLI commands,
// options, protocol flags) are not translated: they are what people search
// logs and docs for.
package i18n

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"sync/atomic"
)

// catalogs maps a language to its translations, keyed by the English text.
// English is the source language and has no catalog.
var catalogs = map[string]map[string]string{"en": nil}

// Register adds translations for a language. Packages call it from init,
// before the interface starts; a text registered twice keeps the last one.
func Register(lang string, msgs map[string]string) {
	c := catalogs[lang]
	if c == nil {
		c = map[string]string{}
		catalogs[lang] = c
	}
	for k, v := range msgs {
		c[k] = v
	}
}

var current atomic.Pointer[string]

func init() {
	en := "en"
	current.Store(&en)
}

// Languages lists the supported language codes.
func Languages() []string {
	var l []string
	for k := range catalogs {
		l = append(l, k)
	}
	sort.Strings(l)
	return l
}

// Set switches the language; "" keeps English.
func Set(lang string) error {
	lang = Normalize(lang)
	if lang == "" {
		lang = "en"
	}
	if _, ok := catalogs[lang]; !ok {
		return fmt.Errorf("unknown language %q, available: %s", lang, strings.Join(Languages(), ", "))
	}
	current.Store(&lang)
	return nil
}

// Lang returns the current language code.
func Lang() string { return *current.Load() }

// Normalize turns "ru_RU.UTF-8" or "RU" into "ru".
func Normalize(lang string) string {
	lang = strings.ToLower(strings.TrimSpace(lang))
	if i := strings.IndexAny(lang, "_.-@"); i >= 0 {
		lang = lang[:i]
	}
	return lang
}

// FromEnv picks a supported language from LC_ALL, LC_MESSAGES or LANG,
// "" when none of them names one.
func FromEnv() string {
	for _, k := range []string{"LC_ALL", "LC_MESSAGES", "LANG"} {
		if v := Normalize(os.Getenv(k)); v != "" {
			if _, ok := catalogs[v]; ok {
				return v
			}
			return ""
		}
	}
	return ""
}

// Has reports whether msg has a translation in lang (from any package).
func Has(lang, msg string) bool {
	_, ok := catalogs[lang][msg]
	return ok
}

// T translates a message.
func T(msg string) string {
	if c := catalogs[Lang()]; c != nil {
		if t, ok := c[msg]; ok {
			return t
		}
	}
	return msg
}

// N marks text stored untranslated and passed through T when shown.
func N(s string) string { return s }

// Tf translates a format string and fills it in.
func Tf(format string, args ...any) string { return fmt.Sprintf(T(format), args...) }
