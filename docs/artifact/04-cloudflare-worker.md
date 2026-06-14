# 04 — Cloudflare Worker 私有分发

> 本节描述**公开/热门**制品的可选第二条下载路径：经 Cloudflare Worker 代理私有 B2 桶 + CDN 缓存，拿到 **B2→Cloudflare 出流量免费**（Bandwidth Alliance）。私有/受控下载默认走预签名 GET（[03 §3](./03-api-design.md#3-获取下载地址)），不需要本节。
>
> 设计照搬作者生产实现：[soft.moe/topic/technology-deploy-r2-cf-worker](https://www.soft.moe/topic/technology-deploy-r2-cf-worker)。**Worker 是 Cloudflare 侧部署的 JS，不在本 Go 仓内**；本仓只负责「该不该返回 Worker URL」。

## 为什么需要它

| | 预签名 GET（默认）| Worker + CF 缓存（本节）|
|--|--|--|
| 适用 | 私有 / 受控 / 一次性下载 | 公开 / 热门、可缓存的大文件 |
| 安全 | 每次签发、短时效 | 私有桶 + Worker 持密钥代理，URL 可长期有效 |
| 缓存 | 每个 URL 唯一，基本不缓存 | Cloudflare 边缘缓存，命中 `Cf-Cache-Status: HIT` |
| 出流量费 | 走 B2 出口（`GetObject` 计费 + egress）| **B2→CF 免费**（Bandwidth Alliance），命中后连 B2 都不碰 |
| 防刷 | 短时效兜底 | 桶私有不可枚举 + 缓存吸收重复请求 |

核心动机（作者原话）：公开桶会被「恶意刷请求数」掏空账单；用 Worker 代理私有桶既能公开访问、又能用 Cloudflare 缓存把 `GetObject` 计费请求挡在边缘。

## 架构

```
终端浏览器
   │  GET https://patch.touchgal.moe/touchgal/<uuid>/setup.zip
   ▼
Cloudflare 边缘（缓存）
   │  MISS → Worker；HIT → 直接回源缓存（不碰 B2）
   ▼
Cloudflare Worker
   │  ① 持 B2 token（b2_authorize_account，缓存，cron 刷新）
   │  ② 路径重写 → /file/<bucket>/<key>
   │  ③ 附 Authorization → fetch 私有桶
   │  ④ 剥离 X-Bz-* 响应头（隐藏后端）
   ▼
Backblaze B2（私有桶 kun-artifacts）
```

## Worker 职责

1. **取并缓存 B2 授权 token**：用 application key 调 `b2_authorize_account`（Basic `keyID:appKey`），拿 `authorizationToken` + `downloadUrl`。token 有效期 24h；用 **Cron Trigger 每天刷两次**（如 `0 7,19 * * *`）写回 Worker 变量/KV，避免每请求都换 token。
2. **路径重写**：把 `https://<cdn_base>/<key>` 重写到 B2 的 `/<downloadUrl>/file/<bucket>/<key>`。本仓的 `<key>` = `{site}/{uuid}/{filename}`（见 [02](./02-storage-and-schema.md)），与 `<cdn_base>` 拼接即原样下载路径。
3. **附鉴权**：给转发请求加 `Authorization: <b2Token>`（header 或 query）。
4. **剥离后端指纹**：删除 `X-Bz-Content-Sha1` 等 `X-Bz-*` 响应头（用 Cloudflare Transform Rules 或在 Worker 内删），不向外暴露 B2 细节。
5. **（可选）防盗链**：校验 `Referer` / 签名参数后再代理。

### Worker 脚本骨架（参考）

```js
// 绑定（加密变量）：B2_KEY_ID, B2_APP_KEY, B2_BUCKET
// KV（或全局缓存）：AUTH 存 {token, downloadUrl}
async function refreshAuth(env) {
  const res = await fetch("https://api.backblazeb2.com/b2api/v2/b2_authorize_account", {
    headers: { Authorization: "Basic " + btoa(`${env.B2_KEY_ID}:${env.B2_APP_KEY}`) },
  });
  const j = await res.json();
  await env.AUTH.put("b2", JSON.stringify({ token: j.authorizationToken, downloadUrl: j.downloadUrl }));
}

export default {
  // Cron: 0 7,19 * * * → 提前刷新 token（24h 有效，留足余量）
  async scheduled(_e, env) { await refreshAuth(env); },

  async fetch(req, env, ctx) {
    const cache = caches.default;
    const hit = await cache.match(req);
    if (hit) return hit;                                   // 缓存命中，不碰 B2

    let auth = JSON.parse((await env.AUTH.get("b2")) || "null");
    if (!auth) { await refreshAuth(env); auth = JSON.parse(await env.AUTH.get("b2")); }

    const key = new URL(req.url).pathname.replace(/^\//, ""); // {site}/{uuid}/{name}
    const upstream = `${auth.downloadUrl}/file/${env.B2_BUCKET}/${key}`;
    let res = await fetch(upstream, { headers: { Authorization: auth.token } });

    // 剥离后端指纹头
    res = new Response(res.body, res);
    for (const h of [...res.headers.keys()]) if (h.toLowerCase().startsWith("x-bz-")) res.headers.delete(h);

    if (res.ok) ctx.waitUntil(cache.put(req, res.clone()));  // 写边缘缓存
    return res;
  },
};
```

> 这是参考骨架，非本仓产物。生产实现可参照官方样例 [backblaze-b2-samples/cloudflare-b2](https://github.com/backblaze-b2-samples/cloudflare-b2)（含 SigV4 签名版本）与作者文章。

## Go 侧如何接入

本仓**不部署 Worker**，只在下载端点决定返回哪种 URL（见 `service.Download`）：

```
若 artifact.public == true 且 该站 oauth_client.artifact_cdn_base 非空：
    返回  <artifact_cdn_base> + "/" + <file_key>      // Worker 域名，CF 缓存
否则：
    返回  presigned GET (~1h, Content-Disposition)     // 私有，每次签
```

- 站点级 `artifact_cdn_base`（如 `https://patch.touchgal.moe`）配在该站的 OAuth Client 行上（[02 站点配置](./02-storage-and-schema.md#站点配置扩展-oauth-client)）。空 → 该站所有下载都走预签名。
- 是否 `public` 由上传方在 `POST /artifacts` 时声明（[03 §1](./03-api-design.md#1-发起上传)）。
- **私有制品永远不要配成 public**：一旦走 Worker 域名，URL 可被缓存/分享，不再有 per-download 鉴权。受控内容请保持 `public=false` 走预签名。

## 部署 checklist（Cloudflare 侧）

1. B2 桶设 **Private**；建一个**只读**（listFiles/readFiles 限定到该桶）application key 给 Worker。
2. Worker 加密变量：`B2_KEY_ID` / `B2_APP_KEY` / `B2_BUCKET`；绑定 KV 命名空间存 token。
3. Cron Trigger：`0 7,19 * * *`（每天两次刷 token）。
4. 自定义域 `patch.<site>.moe` 路由到 Worker；Cloudflare 缓存规则按需设 Edge TTL。
5. Transform Rules：剥离 `X-Bz-*` 响应头（若没在 Worker 内删）。
6. 把该域写进对应站点 `oauth_client.artifact_cdn_base`。

> Worker 用的是**只读**密钥，与本服务的 `presigner`（写）/ `cleanup`（删）密钥三者分离——最小权限（[01 决策 6](./01-design.md#决策-6最小权限密钥presigner--cleanup-分离)）。

下一篇：[05 — 工程计划](./05-engineering-plan.md)
