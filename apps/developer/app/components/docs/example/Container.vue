<script setup lang="ts">
// A worked example over two REAL series (the 2026-07-28 golden-sample
// acceptance data): SEQUEL (doujin, リーフジオメトリ) × いろセカ (commercial,
// FAVORITE). Every response excerpt below is a real value from the live API —
// nothing is mocked. Both series are R18, which conveniently demonstrates the
// caller-controlled nsfw gate (hidden by default).
useSeoMeta({
  title: '实战示例',
  description:
    '用两个真实系列走通 NextMoe 开放 API 全链：标题搜索 → 详情全 facet → 系列/厂牌深链 → 外部 id 反查。'
})

const step1 = `curl "https://api.nextmoe.dev/v1/catalog/search?type=works&q=SEQUEL&nsfw=1" \\
  -H "Authorization: Bearer nm_live_<YOUR_KEY>"`

const step1Resp = `{
  "code": 0,
  "data": {
    "items": [
      {
        "id": 207379,
        "entity_type": "work",
        "name": "この素晴らしい世界に祝福を！このゲーム性に嫉妬を！",
        "latin": "…",
        "content_rating": "r18",
        "sources": ["dlsite:…", "bangumi:…"]
      }
      // …更多命中（SEQUEL 系列 5 员都在索引里）
    ],
    "total": 5
  }
}`

const step2 = `curl "https://api.nextmoe.dev/v1/catalog/works/207379?nsfw=1&include=relations,credits" \\
  -H "Authorization: Bearer nm_live_<YOUR_KEY>"`

const step2Resp = `{
  "data": {
    "id": 207379,
    "display_name": "…",
    "content_rating": "r18",
    "series":     [{ "id": 320, "name": "SEQUEL", "member_count": 5, "source": "dlsite" }],
    "popularity": [
      { "source": "bangumi", "metric": "wish",     "value": 33 },
      { "source": "bangumi", "metric": "collect",  "value": 29 },
      { "source": "dlsite",  "metric": "dl_count", "value": 17710 },
      { "source": "dlsite",  "metric": "wishlist", "value": 17114 }
    ],
    "titles": [{ "kind": "primary", "lang": "ja", "title": "…", "latin": "…" }],
    "labels": [{ "id": 11310, "display_name": "リーフジオメトリ", "kind": "circle" }],
    "relations": [ /* include=relations：双向关系边 + 对端身份 */ ],
    "credits":   [ /* include=credits：署名 staff/cast + 角色 */ ]
    // …intro / tags / covers / screenshots / releases / playtimes / ratings /
    //   characters / platforms / refs —— 12 个 facet 块恒在
  }
}`

const step2b = `// いろセカ家族（w426 紅い瞳）的多源评分与时长（真实值）：
"ratings": [
  { "source": "vndb",         "score": 7.87, "vote_count": 375 },
  { "source": "bangumi",      "score": 6.9,  "vote_count": 1065, "rank": 4184 },
  { "source": "erogamescape", "score": 78,   "vote_count": 446 }
],
"playtimes": [
  { "source": "vndb",         "minutes": 578, "vote_count": 38 },
  { "source": "erogamescape", "minutes": 600 }
]`

const step3 = `curl "https://api.nextmoe.dev/v1/catalog/labels/11310" \\
  -H "Authorization: Bearer nm_live_<YOUR_KEY>"`

const step3Resp = `{
  "data": {
    "id": 11310,
    "display_name": "リーフジオメトリ",
    "kind": "circle",
    "intros": [{ "lang": "ja", "source": "cien", "intro": "ゲーム作品『SEQUEL』シリーズ等の…" }],
    "links": [
      { "source": "official_site", "url": "…" },
      { "source": "twitter",       "url": "…" },
      { "source": "cien",          "url": "…" }
    ]
    // include=works&nsfw=1 可附归属作品列表
  }
}`

const step4 = `curl "https://api.nextmoe.dev/v1/catalog/lookup?source=vndb&external_id=v19658&nsfw=1" \\
  -H "Authorization: Bearer nm_live_<YOUR_KEY>"`
</script>

<template>
  <div class="space-y-10">
    <div>
      <h1 class="text-2xl font-bold text-foreground">实战示例：两个真实系列走通全链</h1>
      <p class="mt-2 text-sm leading-relaxed text-default-500">
        以 <b>SEQUEL</b>（同人 · リーフジオメトリ）与
        <b>いろセカ</b>（商业 · FAVORITE）两个真实系列演示
        搜索 → 详情 → 系列/厂牌深链 → 外部 id 反查。下面每一段响应节选都是线上真实数据。
        两系列均为 R18 —— 正好演示调用方自控的
        <code class="font-mono text-xs">nsfw</code> 参数（缺省隐藏，显式
        <code class="font-mono text-xs">nsfw=1</code> 才可见）。
      </p>
    </div>

    <section class="space-y-3">
      <h2 class="text-lg font-semibold text-foreground">1. 标题搜索（type=works）</h2>
      <pre class="overflow-x-auto rounded-xl border border-default-200 bg-content1 p-4 text-xs leading-relaxed"><code>{{ step1 }}</code></pre>
      <pre class="overflow-x-auto rounded-xl border border-default-200 bg-content1 p-4 text-xs leading-relaxed text-default-500"><code>{{ step1Resp }}</code></pre>
    </section>

    <section class="space-y-3">
      <h2 class="text-lg font-semibold text-foreground">2. 详情全 facet（含系列与热度）</h2>
      <pre class="overflow-x-auto rounded-xl border border-default-200 bg-content1 p-4 text-xs leading-relaxed"><code>{{ step2 }}</code></pre>
      <pre class="overflow-x-auto rounded-xl border border-default-200 bg-content1 p-4 text-xs leading-relaxed text-default-500"><code>{{ step2Resp }}</code></pre>
      <p class="text-sm text-default-500">
        多源数据在同一响应里并列出（按 <code class="font-mono text-xs">source</code> 区分，语义保持源原生）：
      </p>
      <pre class="overflow-x-auto rounded-xl border border-default-200 bg-content1 p-4 text-xs leading-relaxed text-default-500"><code>{{ step2b }}</code></pre>
    </section>

    <section class="space-y-3">
      <h2 class="text-lg font-semibold text-foreground">3. 厂牌 / 社团档案</h2>
      <pre class="overflow-x-auto rounded-xl border border-default-200 bg-content1 p-4 text-xs leading-relaxed"><code>{{ step3 }}</code></pre>
      <pre class="overflow-x-auto rounded-xl border border-default-200 bg-content1 p-4 text-xs leading-relaxed text-default-500"><code>{{ step3Resp }}</code></pre>
    </section>

    <section class="space-y-3">
      <h2 class="text-lg font-semibold text-foreground">4. 外部 id 反查（手握 VNDB/Bangumi/DLsite/EG id 时的首选）</h2>
      <pre class="overflow-x-auto rounded-xl border border-default-200 bg-content1 p-4 text-xs leading-relaxed"><code>{{ step4 }}</code></pre>
      <p class="text-sm text-default-500">
        批量版走 <code class="font-mono text-xs">POST /v1/catalog/lookup/batch</code>。
        全部端点与参数见
        <NuxtLink to="/docs/catalog" class="text-primary hover:underline">Catalog API 文档</NuxtLink>；
        机器可读 spec 在
        <code class="font-mono text-xs">/v1/catalog/openapi.json</code>（免 key）。
        想先不写代码试一试？用
        <NuxtLink to="/explore" class="text-primary hover:underline">数据浏览</NuxtLink>。
      </p>
    </section>
  </div>
</template>
