package getchumedia

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestMirrorPath(t *testing.T) {
	assert.Equal(t, "/data/getchu/1000236/c1000236sample1.jpg",
		mirrorPath("/data/getchu", "1000236", "c1000236sample1.jpg"))
	assert.Equal(t, "/data/getchu/1000236/c1000236sample1.jpg",
		mirrorPath("/data/getchu/", "1000236", "c1000236sample1.jpg"),
		"a trailing slash on the root must not double up")
}

func TestWindowSlicesWholeWorks(t *testing.T) {
	all := []candidate{{WorkID: 1}, {WorkID: 2}, {WorkID: 3}, {WorkID: 4}}
	assert.Equal(t, all, window(all, 0, 0))
	assert.Equal(t, all[:2], window(all, 2, 0))
	assert.Equal(t, all[2:], window(all, 0, 2))
	assert.Equal(t, all[2:3], window(all, 1, 2))
	assert.Nil(t, window(all, 2, 9), "an offset past the end yields nothing")
}
