package personmint

import (
	"testing"

	"api/internal/platform/catalog/model"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestParseBangumiFacts pins the jsonb dirty-value guard (a scalar / null
// Fields must supply nothing, not panic and not be guessed at) and the two
// fields this wave reads out of a healthy infobox.
func TestParseBangumiFacts(t *testing.T) {
	for name, raw := range map[string]string{
		"empty":         ``,
		"json null":     `null`,
		"no fields":     `{"Type":"Person"}`,
		"fields null":   `{"Fields":null}`,
		"fields scalar": `{"Fields":"性别"}`,
		"fields number": `{"Fields":42}`,
		"malformed":     `{"Fields":[`,
	} {
		t.Run(name, func(t *testing.T) {
			f := parseBangumiFacts([]byte(raw))
			assert.Empty(t, f.Gender)
			assert.Nil(t, f.BirthY)
		})
	}

	f := parseBangumiFacts([]byte(`{"Type":"Person","Fields":[
		{"Key":"简体中文名","Value":"北都南"},
		{"Key":"性别","Value":"女"},
		{"Key":"生日","Value":"1978年3月4日"}]}`))
	assert.Equal(t, "女", f.Gender)
	require.NotNil(t, f.BirthY)
	assert.EqualValues(t, 1978, *f.BirthY)
	assert.EqualValues(t, 3, *f.BirthM)
	assert.EqualValues(t, 4, *f.BirthD)
}

// TestParseBirthday covers every shape the staging mirror actually carries,
// including the PARTIAL dates that are the reason the model has three nullable
// columns, and the junk that must yield nothing rather than a guess.
func TestParseBirthday(t *testing.T) {
	for _, tc := range []struct {
		in      string
		y, m, d int
	}{
		{"1978年3月4日", 1978, 3, 4},
		{"1978年03月04日 ", 1978, 3, 4},
		{"1978年3月", 1978, 3, 0},
		{"1978年", 1978, 0, 0},
		{"1978", 1978, 0, 0},
		{"1978-03-04", 1978, 3, 4},
		{"1978-03", 1978, 3, 0},
		{"12月25日", 0, 12, 25},
		{"7月", 0, 7, 0},
		{"1978年3月4日（自称）", 1978, 3, 4},
		{"未知", 0, 0, 0},
		{"", 0, 0, 0},
		{"女×2　ろん×1", 0, 0, 0},
		// Out-of-range components are dropped component-wise, never clamped.
		{"1978年13月4日", 1978, 0, 0},
		{"0500年1月1日", 0, 1, 1},
	} {
		t.Run(tc.in, func(t *testing.T) {
			y, m, d := parseBirthday(tc.in)
			assert.Equal(t, tc.y, deref(y), "year")
			assert.Equal(t, tc.m, deref(m), "month")
			assert.Equal(t, tc.d, deref(d), "day")
		})
	}
}

// TestNormGender pins the two source vocabularies and, above all, that an
// unrecognized value is UNKNOWN — it must neither assert a gender nor count as
// a conflict against the other source.
func TestNormGender(t *testing.T) {
	for _, in := range []string{"m", "男", "男性", "♂"} {
		g, ok := normGender(in)
		require.True(t, ok, in)
		assert.Equal(t, model.GenderMale, g)
	}
	for _, in := range []string{"f", "女", "女性", "♀"} {
		g, ok := normGender(in)
		require.True(t, ok, in)
		assert.Equal(t, model.GenderFemale, g)
	}
	for _, in := range []string{"", "未知", "非二元性别", "女←男", "男、女", "1984年"} {
		_, ok := normGender(in)
		assert.False(t, ok, in)
	}
}

// TestOrgNamePattern pins the organization discriminator: every token was
// surveyed against the live member names, and a person-shaped name must not be
// caught by it.
func TestOrgNamePattern(t *testing.T) {
	for _, in := range []string{
		"株式会社ブリッジ", "有限会社エムツー", "NTTソルマーレ株式会社", "合資会社ワムソフト",
		"バイブリーアニメーションスタジオ", "音楽工房 DOORS", "趣味工房にんじんわいん",
		"Studio e.go!", "AZstudio", "Cats on a Lilypad Studios", "MediBang Inc.", "DOORS, LTD",
		"ALI PROJECT", "Team-OZ", "Design Group Radi", "Purple SOFTWARE", "Mcreate Company",
	} {
		assert.True(t, orgNamePattern.MatchString(in), in)
	}
	for _, in := range []string{
		"北都南", "ひと美", "高橋レコード", "TAMAMI", "神無月如月", "Incognito", "Teamo",
	} {
		assert.False(t, orgNamePattern.MatchString(in), in)
	}
}

func deref(v *int16) int {
	if v == nil {
		return 0
	}
	return int(*v)
}
