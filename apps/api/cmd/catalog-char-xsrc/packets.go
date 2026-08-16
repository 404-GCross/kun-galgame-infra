package main

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"api/internal/jobs/personadj"
	"api/internal/platform/catalog/editspec"

	"gorm.io/gorm"
)

func packetFor(p pairMeta, info map[int64]*charInfo, titles map[int64]string) personadj.Packet {
	bucket := personadj.BucketCharacterPair
	if p.Tier == 2 {
		bucket = personadj.BucketCharacterCV
	}
	meta, _ := json.Marshal(map[string]any{"tier": p.Tier})
	return personadj.Packet{
		Bucket: bucket,
		Key:    fmt.Sprintf("xsrc:%d:%d", p.A, p.B),
		User:   evidenceText(p, info, titles),
		Meta:   meta,
	}
}

func evidenceText(p pairMeta, info map[int64]*charInfo, titles map[int64]string) string {
	a, b := info[p.A], info[p.B]
	var sb strings.Builder

	var ws []string
	for i, w := range p.Works {
		if i >= 3 {
			ws = append(ws, fmt.Sprintf("等 %d 部", len(p.Works)))
			break
		}
		t := titles[w]
		if t == "" {
			t = "(无标题)"
		}
		ws = append(ws, fmt.Sprintf("%s (work %d)", t, w))
	}
	fmt.Fprintf(&sb, "【共同作品】%s\n", strings.Join(ws, " / "))
	if p.CV != "" {
		fmt.Fprintf(&sb, "【声优】两条目在共同作品中由同一署名「%s」配音\n", p.CV)
	}
	renderSide(&sb, "A", a)
	renderSide(&sb, "B", b)
	if p.Instance {
		sb.WriteString("【注意】至少一侧是跨宇宙变体条目(instance),变体与本体不应合并\n")
	}
	return sb.String()
}

func renderSide(sb *strings.Builder, tag string, c *charInfo) {
	fmt.Fprintf(sb, "【条目%s】catalog %d,来源 %s\n", tag, c.ID, strings.Join(c.Anchors, " + "))
	fmt.Fprintf(sb, "名字: %s", c.Name)
	if c.Lang != "" {
		fmt.Fprintf(sb, " (%s)", c.Lang)
	}
	sb.WriteString("\n")
	if len(c.Aliases) > 0 {
		fmt.Fprintf(sb, "别名: %s\n", strings.Join(c.Aliases, "、"))
	}
	if c.Gender != nil {
		fmt.Fprintf(sb, "性别: %s\n", genderLabel(*c.Gender))
	}
	if c.Intro != "" {
		fmt.Fprintf(sb, "简介: %s\n", c.Intro)
	}
}

func genderLabel(g int16) string {
	switch g {
	case 1:
		return "男"
	case 2:
		return "女"
	case 3:
		return "其他/双性"
	}
	return fmt.Sprintf("(%d)", g)
}

func loadWorkTitles(db *gorm.DB, pairs []pairMeta) (map[int64]string, error) {
	ids := map[int64]bool{}
	for _, p := range pairs {
		for i, w := range p.Works {
			if i >= 3 {
				break
			}
			ids[w] = true
		}
	}
	all := make([]int64, 0, len(ids))
	for id := range ids {
		all = append(all, id)
	}
	sort.Slice(all, func(i, j int) bool { return all[i] < all[j] })
	out := make(map[int64]string, len(all))
	for _, chunk := range chunks(all, 10000) {
		type trow struct {
			WorkID int64
			Title  string
		}
		var rows []trow
		if err := db.Raw(`SELECT DISTINCT ON (work_id) work_id, title
			FROM catalog_work_title wt WHERE work_id IN ?
			  AND `+editspec.NotSuppressedWorkTitleSQL("wt")+`
			ORDER BY work_id, CASE lang WHEN 'ja' THEN 0 WHEN 'zh-Hans' THEN 1 ELSE 2 END, kind, id`,
			chunk).Scan(&rows).Error; err != nil {
			return nil, fmt.Errorf("work titles: %w", err)
		}
		for _, r := range rows {
			out[r.WorkID] = r.Title
		}
	}
	return out, nil
}
