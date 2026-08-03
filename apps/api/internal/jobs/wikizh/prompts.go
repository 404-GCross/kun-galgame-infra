package wikizh

// The system prompts are PINNED here (the wave-87 discipline: a prompt change
// is a code change, so a verdict file can be attributed and a re-run wave can
// diff prompt versions).
//
// Both prompts lead with the failure modes this wave MEASURED rather than with
// generic advice:
//
//   - the retired wiki's Chinese is not uniformly good. Sampling found entries
//     whose entire body is a three-character fragment ("深夜、") sitting beside
//     a complete machine translation of the same work. 1,556 of the 3,807
//     bucket-A texts are under 250 characters.
//   - and it is not uniformly bad either, which is why wave 146's blanket
//     "claimed zh is a translation, discard it" ruling had to be walked back in
//     wave 164: plenty are signed original writing by site users.
//
// So neither "the human wrote it, keep it" nor "the machine is newer, keep it"
// is a defensible default, and the judge is asked for a quality comparison
// rather than a provenance classification.
const promptVersion = "wiki-zh-quality-v1"

// PromptVersion identifies the pinned prompt set in every verdict batch.
func PromptVersion() string { return promptVersion }

// usableSystem judges a lone wiki text: is it a usable intro at all?
// (bucket A — the catalog holds no Chinese, so there is nothing to compare
// against and the only question is whether this text is worth publishing.)
const usableSystem = `你是 galgame 资料站的资深编辑。下面给你一部作品的原文简介(日文或英文),以及站内用户早年手写的中文简介。请判断:这段中文是否可以直接作为该作品的中文简介发布?

判定要点(这些是本项目实测出来的高频情况,务必遵守):
1. 只要它是一段**能读的、与该作品相符的**介绍,就判 usable——它不必是原文的逐句翻译。用户手写的介绍常常是改写、缩写或原创导读,这是可以接受的。
2. 判 unusable 的典型:内容被截断只剩残片(如只有一两句话就断了)、只是一个标题或一行标语、纯粹的购买/发售信息、与该作品无关的内容、明显的乱码或占位符。
3. 长度本身不是判据。一段 80 字的完整概述是 usable;一段 200 字但读到一半戛然而止的是 unusable。
4. 带有 markdown 标题、引用块等排版标记是正常的,不影响判定。
5. 含有少量错别字或不通顺的句子仍可判 usable,只要整体信息正确且完整。
6. 拿不准就填 unsure。unsure 会进人工复核,不会造成损失;错误地判 usable 会让残片直接对外发布。

输出要求:只输出一个 JSON 对象,不要代码块围栏、不要多余文字:
{"key":"<原样回填给你的 key>","verdict":"usable|unusable|unsure","confidence":0.0-1.0,"reason":"一句话中文理由"}`

// compareSystem judges wiki text against the machine translation that occupies
// the slot today (bucket B).
const compareSystem = `你是 galgame 资料站的资深编辑。下面给你一部作品的原文简介(日文或英文)、站内用户早年手写的中文简介(A),以及机器翻译生成的中文简介(B)。请判断:作为该作品对外发布的中文简介,A 和 B 哪一个更好?

判定要点(这些是本项目实测出来的高频情况,务必遵守):
1. 首要标准是**对读者的实用价值**:信息是否完整、是否忠实于原文、中文是否自然可读。
2. **A 经常是残片**。实测中存在 A 全文只有三个字而 B 是完整译文的情况。A 只要是被截断的、只剩标题或一句话的,一律判 b_better,不要因为「是人写的」而偏袒。
3. **B 经常是逐字直译**,句式生硬、专有名词处理粗糙。当 A 是完整且通顺的中文时,通常 A 更好——即使 A 是改写而非逐句翻译。
4. 两者信息量与可读性相当时,判 equivalent(此时保留现状,不做改动)。
5. A 与 B 内容明显矛盾时,以**原文**为准来判断谁更忠实。
6. 拿不准就填 unsure。unsure 会进人工复核。

输出要求:只输出一个 JSON 对象,不要代码块围栏、不要多余文字:
{"key":"<原样回填给你的 key>","verdict":"a_better|b_better|equivalent|unsure","confidence":0.0-1.0,"reason":"一句话中文理由"}`

// batchSuffix turns a single-item prompt into a chunked one. Chunking is what
// makes the pass affordable — wave 156 measured the gateway's ceiling to be per
// token rather than per worker, so a chunk of 5 moved throughput from ~18 to
// ~90 judgements a minute.
const batchSuffix = `

本次会一次给你多条,每条以 "### key: <key>" 开头。请对每一条各输出一个 JSON 对象,每行一个(JSON Lines),顺序与输入一致,不要输出任何其他内容。key 必须原样回填。`

// SystemPrompt returns the pinned prompt for a bucket.
func SystemPrompt(b Bucket) string {
	if b == BucketCompare {
		return compareSystem
	}
	return usableSystem
}

// BatchSystemPrompt returns the pinned prompt for a bucket in chunked mode.
func BatchSystemPrompt(b Bucket) string { return SystemPrompt(b) + batchSuffix }
