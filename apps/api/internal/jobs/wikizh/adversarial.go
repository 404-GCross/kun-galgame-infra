package wikizh

// The adversarial pass, for the works three ordinary rounds pointed opposite
// ways on (523 of 7,636: 291 compare, 232 usable).
//
// Re-running the SAME prompt more times would only resample the same coin. Each
// bucket instead gets a re-framing aimed at the specific reason its rounds are
// unstable.
//
// COMPARE — the hypothesis is position bias, and it is testable. Across all
// three rounds the model chose B 8,718 times and A 2,390: an overwhelming
// preference for the machine text, which is either a real quality finding or an
// artefact of B being the text it read last. Swapping the two positions
// separates those:
//
//	verdict follows the TEXT     → the judgement is about the writing; take it
//	verdict follows the POSITION → the two are indistinguishable to the judge,
//	                               so there is no quality gain to bank; keep the
//	                               machine text (the zero-risk default)
//
// Note this also vouches for what already shipped: the 259 promotions won three
// unanimous rounds AGAINST that prevailing preference, which makes them the
// most robust verdicts in the set rather than the least.
//
// USABLE — there is no A/B, so position bias cannot be the cause. Here the
// instability is that "is this publishable?" invites a vibe. The re-framing
// makes it evidentiary: name a concrete disqualifying defect and quote it, or
// concede there is none. A prosecution that cannot produce evidence loses.
const adversarialPromptVersion = "wiki-zh-adversarial-v1"

// AdversarialPromptVersion identifies the pinned adversarial prompt set.
func AdversarialPromptVersion() string { return adversarialPromptVersion }

// compareSwapSystem is compareSystem with the roles exchanged: A is now the
// MACHINE text and B the user's. The verdict is mapped back by SwapVerdict, so
// verdict files stay in the one vocabulary the apply pass understands.
const compareSwapSystem = `你是 galgame 资料站的资深编辑。下面给你一部作品的原文简介(日文或英文),以及两份候选中文简介 A 和 B。请判断:作为该作品对外发布的中文简介,A 和 B 哪一个更好?

判定要点(这些是本项目实测出来的高频情况,务必遵守):
1. 首要标准是**对读者的实用价值**:信息是否完整、是否忠实于原文、中文是否自然可读。
2. **残片一律判负**。存在候选全文只有三个字而另一份是完整介绍的情况。被截断的、只剩标题或一句话的,一律判另一方更好。
3. **逐字直译的句式生硬、专有名词处理粗糙**(如人名假名原样留在中文里)。当另一份是完整且通顺的中文时,通常那一份更好——即使它是改写而非逐句翻译。
4. 两者信息量与可读性相当时,判 equivalent。
5. 两者内容明显矛盾时,以**原文**为准来判断谁更忠实。
6. 不要因为某一份看起来更"正式"或更长就偏袒它;只看上面五条。
7. 拿不准就填 unsure。

输出要求:只输出一个 JSON 对象,不要代码块围栏、不要多余文字:
{"key":"<原样回填给你的 key>","verdict":"a_better|b_better|equivalent|unsure","confidence":0.0-1.0,"reason":"一句话中文理由"}`

// usableAdversarialSystem asks for evidence instead of an impression.
const usableAdversarialSystem = `你是 galgame 资料站的资深编辑,现在担任**审查方**。下面给你一部作品的原文简介(日文或英文),以及站内用户早年手写的中文简介。

你的任务不是给出印象,而是**举证**:这段中文是否存在**足以阻止它对外发布**的具体缺陷?

只有以下几类算作"足以阻止发布"的缺陷,且你必须从原文中**引用**触发该判定的片段:
1. 内容被截断只剩残片(只有一两句就断了、读到一半戛然而止)。
2. 它根本不是简介(只是标题、标语、购买/发售信息、编辑备注)。
3. 内容与该作品无关(讲的是别的作品)。
4. 乱码、占位符、模板残留。
5. **经英文转译的机翻残次品**:人名/专有名词以拉丁字母原样留在中文里、同一角色出现两个不同译名、日文台词被转译成不知所云的中文。

不构成缺陷、**不得**据此判 unusable 的情况:
- 它是改写、缩写或原创导读,而非原文的逐句翻译(这是可以接受的)。
- 篇幅短但内容完整。
- 少量错别字、个别句子不顺。
- 带 markdown 标题、引用块等排版标记。
- 你个人觉得写得不够精彩。

判定规则:**举不出上述任一缺陷并附引用,就判 usable。** 举得出就判 unusable,并在 reason 里写明是第几类以及引用的片段。确实无法判断才填 unsure。

输出要求:只输出一个 JSON 对象,不要代码块围栏、不要多余文字:
{"key":"<原样回填给你的 key>","verdict":"usable|unusable|unsure","confidence":0.0-1.0,"reason":"一句话中文理由(判 unusable 必须含缺陷类别与引用)"}`

// AdversarialSystemPrompt returns the pinned adversarial prompt for a bucket.
func AdversarialSystemPrompt(b Bucket) string {
	if b == BucketCompare {
		return compareSwapSystem
	}
	return usableAdversarialSystem
}

// BatchAdversarialSystemPrompt is the chunked form.
func BatchAdversarialSystemPrompt(b Bucket) string {
	return AdversarialSystemPrompt(b) + batchSuffix
}

// SwapVerdict maps a verdict produced under swapped positions back onto the
// canonical vocabulary, where a_better always means "the user's text wins".
// Applied the moment the reply is parsed, so nothing downstream — the verdict
// file, the fold, the apply pass — ever has to know a swap happened.
func SwapVerdict(v string) string {
	switch v {
	case VerdictABetter:
		return VerdictBBetter
	case VerdictBBetter:
		return VerdictABetter
	default:
		return v
	}
}

// AdversarialPacket renders a candidate for the adversarial pass. For compare
// it exchanges the two texts; for usable it is the ordinary packet, since the
// re-framing there lives entirely in the system prompt.
func AdversarialPacket(c Candidate) string {
	if c.Bucket != BucketCompare {
		return UserPacket(c)
	}
	swapped := c
	swapped.WikiZh, swapped.MachineZh = c.MachineZh, c.WikiZh
	return UserPacket(swapped)
}
