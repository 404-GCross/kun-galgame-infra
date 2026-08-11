package ymgalnews

import (
	"encoding/json"
	"testing"
)

// realPage is a verbatim page-1 item captured from the live endpoint on
// 2026-08-11. It exists because the published documentation types topicId as
// int64 while the API sends it as a JSON string: decoding it as a number fails
// outright, so this fixture is the record of what the wire actually carries.
const realPage = `{
  "success": true,
  "code": 0,
  "msg": null,
  "data": [
    {
      "topicId": "873862231488462848",
      "author": 6844,
      "mainImg": "https://cdn.ymgal.games/topic/main/0d/0dbf9225a5a74aeb9917c0c3233ca7e8.webp",
      "topicUrl": "https://www.ymgal.games/co/article/873862231488462848",
      "title": "Anemoi",
      "introduction": "前两天刊载每月小奏的G's板块",
      "views": 2953,
      "replyNum": 2,
      "likesNum": 0,
      "favoritesNum": 0,
      "publishTime": "2026-08-08 09:36:31",
      "createAt": "新月酱",
      "topicCategory": "资讯"
    }
  ]
}`

func TestDecodeRealPage(t *testing.T) {
	var env envelope
	if err := json.Unmarshal([]byte(realPage), &env); err != nil {
		t.Fatalf("envelope: %v", err)
	}
	if !env.Success || env.Code != 0 {
		t.Fatalf("success=%v code=%d", env.Success, env.Code)
	}
	var topics []Topic
	if err := json.Unmarshal(env.Data, &topics); err != nil {
		t.Fatalf("data is a bare array of topics: %v", err)
	}
	if len(topics) != 1 {
		t.Fatalf("got %d topics", len(topics))
	}
	got := topics[0]
	if got.TopicID != "873862231488462848" {
		t.Errorf("topicId = %q; it is a snowflake sent as a STRING despite the docs saying int64", got.TopicID)
	}
	if got.CreateAt != "新月酱" {
		t.Errorf("createAt = %q; it carries the AUTHOR NAME, not a timestamp", got.CreateAt)
	}
	if got.TopicCategory != "资讯" {
		t.Errorf("topicCategory = %q", got.TopicCategory)
	}
}

// TestExhaustedPageDecodes pins the only stop signal the upstream gives us:
// there is no total and no hasNext, just an empty array with code 0.
func TestExhaustedPageDecodes(t *testing.T) {
	var env envelope
	if err := json.Unmarshal([]byte(`{"success":true,"code":0,"msg":null,"data":[]}`), &env); err != nil {
		t.Fatalf("envelope: %v", err)
	}
	var topics []Topic
	if err := json.Unmarshal(env.Data, &topics); err != nil {
		t.Fatalf("empty data: %v", err)
	}
	if len(topics) != 0 {
		t.Errorf("got %d topics, want 0", len(topics))
	}
}

func TestLanePath(t *testing.T) {
	for lane, want := range map[string]string{LaneNews: pathNews, LaneColumn: pathColumn} {
		got, err := lanePath(lane)
		if err != nil || got != want {
			t.Errorf("lanePath(%q) = %q, %v; want %q", lane, got, err, want)
		}
	}
	if _, err := lanePath("weekly"); err == nil {
		t.Error("an unknown lane must be rejected, not silently mapped to a path")
	}
}
