package virtualmedia

import "strings"

// legacyChromiumFlagPrefixes are Chromium flags we explicitly avoid because they
// drive fake media devices instead of OS-level virtual devices.
var legacyChromiumFlagPrefixes = []string{
	"--use-fake-device-for-media-stream",
	"--use-file-for-fake-video-capture",
	"--use-file-for-fake-audio-capture",
	"--use-fake-ui-for-media-stream",
}

// IsLegacyChromiumFlag returns true when the provided token matches one of the
// fake-media flag prefixes we intentionally strip out.
func IsLegacyChromiumFlag(token string) bool {
	trimmed := strings.TrimSpace(token)
	if trimmed == "" {
		return false
	}
	for _, prefix := range legacyChromiumFlagPrefixes {
		if strings.HasPrefix(trimmed, prefix) {
			return true
		}
	}
	return false
}

// FilterLegacyChromiumFlags drops any fake-media flags from the provided slice
// and trims whitespace. Empty tokens are also removed.
func FilterLegacyChromiumFlags(tokens []string) []string {
	out := make([]string, 0, len(tokens))
	for _, tok := range tokens {
		trimmed := strings.TrimSpace(tok)
		if trimmed == "" || IsLegacyChromiumFlag(trimmed) {
			continue
		}
		out = append(out, trimmed)
	}
	return out
}
