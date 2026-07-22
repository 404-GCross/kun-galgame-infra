package charattrs

import (
	"testing"

	"api/internal/platform/catalog/model"

	"github.com/stretchr/testify/assert"
	"gorm.io/datatypes"
)

func deref(p *int16) int16 {
	if p == nil {
		return -1 // sentinel for "nil" in table assertions
	}
	return *p
}
func derefS(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// --- VNDB decoders ---

func TestVNDBSexGender(t *testing.T) {
	cases := map[string]int16{
		"m": model.GenderMale, "f": model.GenderFemale, "M": model.GenderMale,
		"b": model.GenderOther, "n": model.GenderOther, "": -1, "  ": -1,
	}
	for in, want := range cases {
		assert.Equal(t, want, deref(vndbSexGender(in)), "sex=%q", in)
	}
}

func TestVNDBBlood(t *testing.T) {
	cases := map[string]int16{
		"a": model.BloodTypeA, "b": model.BloodTypeB, "ab": model.BloodTypeAB,
		"o": model.BloodTypeO, "unknown": -1, "": -1,
	}
	for in, want := range cases {
		assert.Equal(t, want, deref(vndbBlood(in)), "bloodt=%q", in)
	}
}

func TestVNDBBirthday(t *testing.T) {
	m, d := vndbBirthday(920)
	assert.Equal(t, int16(9), deref(m))
	assert.Equal(t, int16(20), deref(d))
	m, d = vndbBirthday(101)
	assert.Equal(t, int16(1), deref(m))
	assert.Equal(t, int16(1), deref(d))
	for _, bad := range []int16{0, -5, 1350 /*m=13*/, 900 /*d=0*/, 1500} {
		m, d = vndbBirthday(bad)
		assert.Nil(t, m, "birthday=%d month", bad)
		assert.Nil(t, d, "birthday=%d day", bad)
	}
}

func TestVNDBGatesAndCup(t *testing.T) {
	assert.Equal(t, int16(165), deref(vndbGate(165, minHeight, maxHeight)))
	assert.Nil(t, vndbGate(0, minHeight, maxHeight), "0 = unset")
	assert.Nil(t, vndbGate(9387, minHeight, maxHeight), "garbage out of range")
	assert.Nil(t, vndbGate(5, minBWH, maxBWH), "below BWH gate")
	assert.Equal(t, "D", derefS(vndbCup("d")))
	assert.Equal(t, "AA", derefS(vndbCup("AA")))
	assert.Nil(t, vndbCup(""), "empty cup")
}

// --- BGM parsers ---

func TestParseBGMBirthday(t *testing.T) {
	type want struct {
		m, d    int16
		keepRaw bool
	}
	cases := map[string]want{
		"6月17日":      {6, 17, false},
		"3月3日":       {3, 3, false},
		"2000年6月17日": {6, 17, true},  // year dropped from columns → keep raw
		"6月17日（B型）":  {6, 17, true},  // trailing text → keep raw
		"6月":         {6, -1, false}, // month-only, clean
		"不明":         {-1, -1, false},
		"？":          {-1, -1, false},
		"夏":          {-1, -1, true}, // non-sentinel, unparseable
		"1月35日":      {1, -1, true},  // day out of range → keep raw
		"2月14日ごろ":    {2, 14, true},  // trailing text
	}
	for in, w := range cases {
		got := parseBGMBirthday(in)
		assert.Equal(t, w.m, deref(got.month), "birthday %q month", in)
		assert.Equal(t, w.d, deref(got.day), "birthday %q day", in)
		assert.Equal(t, w.keepRaw, got.keepRaw, "birthday %q keepRaw", in)
	}
}

func TestParseBGMBlood(t *testing.T) {
	cases := map[string]int16{
		"A型": model.BloodTypeA, "O型": model.BloodTypeO, "B型": model.BloodTypeB,
		"AB型": model.BloodTypeAB, "A": model.BloodTypeA, "Ａ型": model.BloodTypeA,
		"ＡＢ型": model.BloodTypeAB, "X型": -1, "F型": -1, "不明": -1, "？": -1, "": -1,
	}
	for in, want := range cases {
		assert.Equal(t, want, deref(parseBGMBlood(in)), "blood=%q", in)
	}
}

func TestBGMGender(t *testing.T) {
	cases := map[string]int16{
		"男": model.GenderMale, "男性": model.GenderMale, "雄": model.GenderMale,
		"雄性": model.GenderMale, "♂": model.GenderMale, "公": model.GenderMale,
		"女": model.GenderFemale, "女性": model.GenderFemale, "雌": model.GenderFemale,
		"♀": model.GenderFemale, "母": model.GenderFemale,
		"男/女": -1, "男→女": -1, "雄性50%｜雌性50%": -1, // both markers → not asserted
		"无性别": -1, "扶她": -1, "不明": -1, "？": -1, "": -1, // neither / sentinel
	}
	for in, want := range cases {
		assert.Equal(t, want, deref(bgmGender(in)), "gender=%q", in)
	}
}

func TestParseBGMMeasure(t *testing.T) {
	h := parseBGMMeasure("165cm", minHeight, maxHeight)
	assert.True(t, h.inRange)
	assert.Equal(t, int16(165), deref(h.value))

	w := parseBGMMeasure("48.5kg", minWeight, maxWeight)
	assert.True(t, w.inRange)
	assert.Equal(t, int16(49), deref(w.value), "rounded")

	oor := parseBGMMeasure("9999cm", minHeight, maxHeight)
	assert.True(t, oor.found)
	assert.False(t, oor.inRange, "out of range")
	assert.Nil(t, oor.value)

	none := parseBGMMeasure("不明", minHeight, maxHeight)
	assert.False(t, none.found)
}

func TestParseBGMBWH(t *testing.T) {
	got := parseBGMBWH("B87/W59/H88")
	assert.Equal(t, int16(87), deref(got.bust))
	assert.Equal(t, int16(59), deref(got.waist))
	assert.Equal(t, int16(88), deref(got.hip))
	assert.Nil(t, got.cup)

	got = parseBGMBWH("87/59/88")
	assert.Equal(t, int16(87), deref(got.bust))
	assert.Equal(t, int16(88), deref(got.hip))

	got = parseBGMBWH("B85(E)/W58/H86")
	assert.Equal(t, int16(85), deref(got.bust))
	assert.Equal(t, "E", derefS(got.cup), "embedded cup extracted")

	got = parseBGMBWH("B92/ W58/ H88")
	assert.Equal(t, int16(92), deref(got.bust), "spaces tolerated")
	assert.Equal(t, int16(58), deref(got.waist))

	got = parseBGMBWH("Cカップ")
	assert.Equal(t, "C", derefS(got.cup), "cup-only")
	assert.Nil(t, got.bust)
	assert.False(t, got.oor)

	got = parseBGMBWH("B??/ W??/ H??")
	assert.Nil(t, got.bust, "?? placeholders → nothing")
	assert.False(t, got.oor)

	got = parseBGMBWH("B999/W58/H88")
	assert.Nil(t, got.bust, "999 out of range")
	assert.Equal(t, int16(58), deref(got.waist))
	assert.True(t, got.oor)
}

// --- exclusion set ---

func TestExclusion(t *testing.T) {
	excluded := []string{"别名", "简体中文名", "引用来源", "CV", "声优", "动画版CV", "PC版CV", "配音", "画师", "中文CV"}
	for _, k := range excluded {
		assert.True(t, isExcludedKey(k), "%q should be excluded", k)
	}
	// Attributes that MUST reach extra (not identity/alias/VA despite containing 名/来源).
	kept := []string{"星座", "年龄", "属性", "趣味", "能力名", "攻击名", "スキル名", "压力来源", "种族", "职业"}
	for _, k := range kept {
		assert.False(t, isExcludedKey(k), "%q should NOT be excluded", k)
		assert.False(t, isPromotionKey(k), "%q is long-tail, not promotion", k)
	}
}

// --- infobox walk: promotion + extra + preservation ---

func TestParseBGMInfobox(t *testing.T) {
	raw := datatypes.JSON(`{"Type":"Crt","Fields":[
		{"Key":"性别","Value":"女"},
		{"Key":"生日","Value":"2000年6月17日"},
		{"Key":"血型","Value":"A型"},
		{"Key":"身高","Value":"9999cm"},
		{"Key":"体重","Value":"48kg"},
		{"Key":"BWH","Value":"B85(E)/W58/H86"},
		{"Key":"别名","Value":"nickname","Array":false},
		{"Key":"CV","Value":"声優さん"},
		{"Key":"星座","Value":"双子座"},
		{"Key":"能力","Value":"","Array":true,"Items":[{"Key":"","Value":"飞行"},{"Key":"","Value":"隐身"}]}
	]}`)
	res := parseBGMInfobox(raw)

	assert.Equal(t, model.GenderFemale, deref(res.attrs.gender))
	assert.Equal(t, int16(6), deref(res.attrs.month))
	assert.Equal(t, int16(17), deref(res.attrs.day))
	assert.Equal(t, model.BloodTypeA, deref(res.attrs.blood))
	assert.Nil(t, res.attrs.height, "9999cm out of range")
	assert.Equal(t, int16(48), deref(res.attrs.weight))
	assert.Equal(t, int16(85), deref(res.attrs.bust))
	assert.Equal(t, "E", derefS(res.attrs.cup))
	assert.Equal(t, 1, res.outOfRange, "the 9999cm height")

	// extra: long-tail + preserved raws; excluded keys absent.
	assert.Equal(t, "双子座", res.extra["星座"])
	assert.Equal(t, []string{"飞行", "隐身"}, res.extra["能力"], "Array folded")
	assert.Equal(t, "2000年6月17日", res.extra["生日"], "year preserved")
	assert.Equal(t, "9999cm", res.extra["身高"], "out-of-range raw preserved")
	assert.NotContains(t, res.extra, "别名", "alias excluded")
	assert.NotContains(t, res.extra, "CV", "VA excluded")
	assert.NotContains(t, res.extra, "性别", "clean promotion not duplicated to extra")
	assert.NotContains(t, res.extra, "血型")
	assert.NotContains(t, res.extra, "体重")
	assert.NotContains(t, res.extra, "BWH", "cup fully consumed")
}
