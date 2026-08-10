package utils

func ParseContentLimit(raw, defaultWhenEmpty string) string {
	switch raw {
	case "sfw", "nsfw":
		return raw
	case "all":
		return ""
	default:
		return defaultWhenEmpty
	}
}
