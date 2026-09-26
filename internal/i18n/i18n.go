// Package i18n holds the two languages the tracker speaks, Japanese and English.
//
// Messages meant for people (API errors, scan errors) are made with New or Errorf and carry both
// languages; the web API answers in the language the page asks for (Text). The console (logs,
// command-line help) speaks Lang, picked once at start (L).
package i18n

import (
	"errors"
	"fmt"
	"os"
	"strings"
)

// Msg is a message in Japanese and English. As an error it reads in Japanese.
type Msg struct{ Ja, En string }

func (m Msg) Error() string { return m.Ja }

// New is a message in both languages.
func New(ja, en string) Msg { return Msg{ja, en} }

// Errorf formats a message in both languages with the same arguments.
func Errorf(ja, en string, args ...any) Msg {
	return Msg{fmt.Sprintf(ja, args...), fmt.Sprintf(en, args...)}
}

// Text is err's message in lang: a Msg in that language ("en" or "ja"), any other error as it is.
func Text(err error, lang string) string {
	var m Msg
	if errors.As(err, &m) {
		if lang == "en" {
			return m.En
		}
		return m.Ja
	}
	return err.Error()
}

// Lang is the console's language: "ja" or "en".
var Lang = "ja"

// L is the text in the console's language.
func L(ja, en string) string {
	if Lang == "en" {
		return en
	}
	return ja
}

// Pick turns a setting ("ja", "en", or anything else for the system's language) into a language.
func Pick(setting string) string {
	if setting == "ja" || setting == "en" {
		return setting
	}
	return systemLanguage()
}

// fromEnv reads a POSIX locale (LC_ALL, LC_MESSAGES, LANG); ok is false when none is set.
func fromEnv() (lang string, ok bool) {
	for _, k := range []string{"LC_ALL", "LC_MESSAGES", "LANG"} {
		if v := os.Getenv(k); v != "" {
			if strings.HasPrefix(strings.ToLower(v), "ja") {
				return "ja", true
			}
			return "en", true
		}
	}
	return "", false
}
