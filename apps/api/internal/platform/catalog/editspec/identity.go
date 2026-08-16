package editspec

import (
	"fmt"
	"strconv"
	"strings"

	"api/internal/platform/textnorm"
)

const (
	TagPerson     = "person"
	TagCreditName = "credit_name"
	TagLabel      = "label"
	TagCharacter  = "character"
	TagWork       = "work"
)

func kindLangTextKeyCheck(prefix string) func(string) error {
	return func(key string) error {
		parts := strings.SplitN(key, ":", 4)
		if len(parts) != 4 || parts[0] != prefix {
			return fmt.Errorf("%q is not an identity key of this field: expected %s:<kind>:<lang>:<text>", key, prefix)
		}
		if _, err := strconv.ParseInt(parts[1], 10, 16); err != nil {
			return fmt.Errorf("%q is not an identity key of this field: %q is not a numeric kind", key, parts[1])
		}
		if parts[3] == "" {
			return fmt.Errorf("%q is not an identity key of this field: the text segment is empty", key)
		}
		if clean := textnorm.Clean(parts[3]); clean != parts[3] {
			return fmt.Errorf("%q is not the canonical form of this key: the text segment carries invisible or edge whitespace, "+
				"so it would suppress nothing; send %q instead",
				key, strings.Join([]string{parts[0], parts[1], parts[2], clean}, ":"))
		}
		return nil
	}
}
