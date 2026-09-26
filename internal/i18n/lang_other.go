//go:build !windows

package i18n

// systemLanguage is Japanese when the locale (LC_ALL, LC_MESSAGES, LANG) says so.
func systemLanguage() string {
	if lang, ok := fromEnv(); ok {
		return lang
	}
	return "en"
}
