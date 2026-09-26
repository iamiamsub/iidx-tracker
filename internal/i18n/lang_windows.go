package i18n

import "syscall"

// systemLanguage is Japanese when Windows' display language is (or a locale variable says so).
func systemLanguage() string {
	if lang, ok := fromEnv(); ok {
		return lang
	}
	id, _, _ := syscall.NewLazyDLL("kernel32.dll").NewProc("GetUserDefaultUILanguage").Call()
	if id&0x3ff == 0x11 { // LANG_JAPANESE
		return "ja"
	}
	return "en"
}
