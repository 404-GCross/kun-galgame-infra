package charportraits

import (
	"fmt"
	"strconv"
	"strings"
)

const chPrefix = "ch"

func chRelPath(id string) (string, error) {
	num := strings.TrimPrefix(id, chPrefix)
	n, err := strconv.Atoi(num)
	if err != nil {
		return "", fmt.Errorf("bad character image id %q", id)
	}
	return fmt.Sprintf("ch/%02d/%s.jpg", n%100, num), nil
}
