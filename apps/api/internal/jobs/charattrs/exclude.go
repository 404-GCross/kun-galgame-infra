package charattrs

import "strings"

const (
	keyBirthday   = "生日"
	keyBlood      = "血型"
	keyHeight     = "身高"
	keyWeight     = "体重"
	keyBWH        = "BWH"
	keyGenderHans = "性别"
	keyGenderHant = "性別"
)

func isGenderKey(k string) bool { return k == keyGenderHans || k == keyGenderHant }

func isPromotionKey(k string) bool {
	switch k {
	case keyBirthday, keyBlood, keyHeight, keyWeight, keyBWH, keyGenderHans, keyGenderHant:
		return true
	default:
		return false
	}
}

var excludedKeys = map[string]bool{
	"别名": true, "別名": true, "别称": true, "別稱": true, "别號": true, "别号": true,
	"简体中文名": true, "簡體中文名": true, "中文名": true, "第二中文名": true, "繁体中文名": true,
	"英文名": true, "日文名": true, "韩文名": true, "韓文名": true, "德文名": true, "法文名": true,
	"拉丁字母名": true, "纯假名": true, "純假名": true, "假名": true, "罗马字": true, "羅馬字": true,
	"昵称": true, "暱稱": true, "爱称": true, "愛称": true, "愛稱": true, "小名": true,
	"真名": true, "本名": true, "原名": true, "旧名": true, "舊名": true, "曾用名": true,
	"通称": true, "通稱": true, "通名": true, "译名": true, "譯名": true, "外号": true, "外號": true,
	"绰号": true, "綽號": true, "二つ名": true, "固有二つ名": true, "名乗り": true, "あだ名": true,
	"呼称/あだ名": true, "呼称": true, "呼稱": true, "粉丝名": true, "粉絲名": true, "论坛名": true,
	"名号": true, "名號": true, "稱號": true, "称号": true, "諢名": true, "浑名": true,
	"引用来源": true, "引用來源": true, "引用来源2": true, "引用來源2": true,
	"来源": true, "來源": true, "来源2": true, "來源2": true, "資料來源": true, "资料来源": true,
	"画师": true, "畫師": true, "插画": true, "插畫": true,
}

func isVACredit(k string) bool {
	if strings.Contains(k, "CV") || strings.Contains(k, "Cv") {
		return true
	}
	for _, tok := range []string{"声优", "声優", "聲優", "配音", "声のキャス", "ボイス"} {
		if strings.Contains(k, tok) {
			return true
		}
	}
	return false
}

func isExcludedKey(k string) bool {
	return excludedKeys[k] || isVACredit(k)
}
