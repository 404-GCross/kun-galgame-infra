package charportraits

import (
	"fmt"
	"strconv"
	"strings"
)

// chPrefix is the VNDB character-image id prefix ("ch175652").
const chPrefix = "ch"

// chRelPath maps a VNDB character-portrait image id ("ch175652") to its path
// relative to the vndb-img rsync root, mirroring the t.vndb.org layout:
// ch/<id mod 100, 2-digit>/<id>.jpg. Identical bucketing to the sibling cover
// (cv/) and screenshot (sf/) jobs, so the ops rsync --files-from list and this
// reader agree byte-for-byte. The generated list must be produced with the SAME
// formula (see the step-48 report's rsync section).
func chRelPath(id string) (string, error) {
	num := strings.TrimPrefix(id, chPrefix)
	n, err := strconv.Atoi(num)
	if err != nil {
		return "", fmt.Errorf("bad character image id %q", id)
	}
	return fmt.Sprintf("ch/%02d/%s.jpg", n%100, num), nil
}
