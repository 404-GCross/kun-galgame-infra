# 安全 / 滥用 / 合规

> 本文承载 §11 安全 / 滥用 / 合规,含 **来源投影(D1 再分发授权)**——公开投影 = 聚合记录,不做逐源原始字段批量再分发。设计与命名约定见 [01-design.md](./01-design.md);决策记录见 [01 §15](./01-design.md)。

---

## 11. 安全 / 滥用 / 合规

- **HTTPS 强制**(Cloudflare);key 只走 header,**不进 URL、不进日志**(日志只留 `key_prefix`)。
- **NSFW**:默认 `sfw`;放开需 `galgame:nsfw` scope + `nsfw_allowed` tier,并审计——NSFW 闸控是**合规问题**(ToS / 法律),不只是整洁问题。catalog 面同理:`content_rating=r18` 的作品行默认过滤,同一 scope 闸控。
- **来源投影(再分发授权,D1 已拍板 2026-07-14)**:公开投影 = **聚合记录**——一个 Galgame 的每个字段是多源归并的结果(名称可能来自 wiki 策展、简介来自 Bangumi、日期来自 VNDB),**不做任何逐源原始字段的批量再分发**;评分以逐源数值 + 归源链接形态出现(P-★ 窄片同款),响应携带 `attribution` 块。归并结果与自产字段(中文简介/tag 本地化/竖图/stats)是投影本体;per-field provenance 机制用于执行该姿态。
- **CORS**:`api.nextmoe.dev` 对浏览器直连**不开放任意 origin 携带 API key**(key 是机密,仅服务端);浏览器场景走 OAuth2 public client + PKCE。
- **ToS / 滥用**:服务条款 + 异常用量告警 + 一键吊销 key/应用。
- **审计**:key 创建/轮换/吊销、tier 变更、异常 4xx/5xx 速率,写审计日志。
