package errors

// 错误码按模块分段
// 1-999: 通用错误
// 10000-19999: Auth
// 15000-15999: OAuth
// 20000-29999: Game
// 30000-39999: Content
// 40000-49999: Comment
// 50000-59999: Artifact
// 60000-69999: Moderation
// 70000-79999: Site
// 80000-80999: Image service

const (
	// 通用错误 (1-999)
	ErrBadRequest       = 1  // 请求格式错误
	ErrInvalidID        = 2  // 无效的 ID
	ErrInternalServer   = 3  // 服务器内部错误
	ErrNotFound         = 4  // 资源不存在
	ErrForbidden        = 5  // 访问被拒绝
	ErrTooManyRequests  = 6  // 请求过于频繁
	ErrValidationFailed = 7  // 参数验证失败
	ErrMissingParam     = 8  // 缺少必要参数
	ErrInvalidParam     = 9  // 参数格式错误
	ErrOperationFailed  = 10 // 操作失败

	// Auth (10000-19999)
	ErrAuthUnauthorized     = 10001
	ErrAuthInvalidToken     = 10002
	ErrAuthTokenExpired     = 10003
	ErrAuthInvalidPassword  = 10004
	ErrAuthUserNotFound     = 10005
	ErrAuthEmailExists      = 10006
	ErrAuthNameExists       = 10007
	ErrAuthPasswordRequired = 10008
	ErrAuthInvalidEmail     = 10009
	ErrAuthCodeInvalid              = 10010
	ErrAuthCodeExpired              = 10011
	ErrAuthEmailChangeTooFrequent   = 10012
	ErrAuthEmailSameAsCurrent       = 10013
	ErrAuthUserBanned               = 10014

	// OAuth (15000-15999)
	ErrOAuthInvalidClient       = 15001
	ErrOAuthInvalidRedirectURI  = 15002
	ErrOAuthInvalidCode         = 15003
	ErrOAuthInvalidCodeVerifier = 15004
	ErrOAuthInvalidGrant        = 15005
	ErrOAuthInvalidScope        = 15006
	ErrOAuthAccessDenied        = 15007
	ErrOAuthInvalidClientSecret = 15008
	ErrOAuthPKCERequired        = 15009
	ErrOAuthConsentRequired     = 15010

	// Moemoepoint (16000-16999)
	ErrMoemoepointInvalidDelta  = 16002
	ErrMoemoepointInvalidReason = 16003
	ErrMoemoepointIdemConflict  = 16004

	// Galgame (20000-29999)
	ErrGalgameNotFound           = 20001
	ErrGalgameAlreadyExists      = 20002
	ErrGalgameInvalidVNDB        = 20003
	ErrGalgameVNDBExists         = 20004
	ErrGalgameForbidden          = 20005
	ErrGalgameClaimNotDraft      = 20006 // claim 时目标 status ≠ 2
	ErrGalgameSubmitterOnly      = 20007 // PATCH/DELETE 时不是提交者本人
	ErrGalgameDraftStatusInvalid = 20008 // 草稿端点：status ∉ {3,4}
	ErrGalgameQuotaExceeded      = 20009 // 今日投稿配额已用尽

	// Artifact (50000-59999)
	ErrArtifactNotFound   = 50001
	ErrArtifactInvalid    = 50002
	ErrArtifactVirusFound = 50003
	ErrArtifactTooBig     = 50004
	ErrArtifactProcessing = 50005

	// Moderation (60000-69999)
	ErrModerationPending  = 60001
	ErrModerationRejected = 60002
	ErrModerationNotFound = 60003

	// Site (70000-79999)
	ErrSiteNotFound      = 70001
	ErrSiteAlreadyExists = 70002

	// Image service (80000-80999)
	ErrImageUnauthorized     = 80001
	ErrImageBadClient        = 80002
	ErrImageBadSecret        = 80003
	ErrImageSiteDisabled     = 80004
	ErrImageSiteUnconfigured = 80005
	ErrImagePresetDenied     = 80006
	ErrImageFileTooLarge     = 80007
	ErrImageQuotaExceeded    = 80008
	ErrImageMIMEDenied       = 80009
	ErrImageDecodeFailed     = 80010
	ErrImagePresetNotFound   = 80011
	ErrImageStoreFailed      = 80012
	ErrImageNotFound         = 80013
	ErrImageBadRequest       = 80014
	ErrImageUploadDisabled   = 80015
)

// 错误码到消息的映射
var codeMessages = map[int]string{
	// 通用错误
	ErrBadRequest:       "请求格式错误",
	ErrInvalidID:        "无效的 ID",
	ErrInternalServer:   "服务器内部错误",
	ErrNotFound:         "资源不存在",
	ErrForbidden:        "访问被拒绝",
	ErrTooManyRequests:  "请求过于频繁",
	ErrValidationFailed: "参数验证失败",
	ErrMissingParam:     "缺少必要参数",
	ErrInvalidParam:     "参数格式错误",
	ErrOperationFailed:  "操作失败",

	// Auth
	ErrAuthUnauthorized:     "未授权，请先登录",
	ErrAuthInvalidToken:     "无效的令牌",
	ErrAuthTokenExpired:     "令牌已过期，请重新登录",
	ErrAuthInvalidPassword:  "邮箱或密码错误",
	ErrAuthUserNotFound:     "用户不存在",
	ErrAuthEmailExists:      "该邮箱已被注册",
	ErrAuthNameExists:       "该用户名已被使用",
	ErrAuthPasswordRequired: "需要重置密码",
	ErrAuthInvalidEmail:     "邮箱格式不正确",
	ErrAuthCodeInvalid:            "验证码无效",
	ErrAuthCodeExpired:            "验证码已过期",
	ErrAuthEmailChangeTooFrequent: "邮箱验证码发送过于频繁，请稍后再试",
	ErrAuthEmailSameAsCurrent:     "新邮箱与当前邮箱相同",
	ErrAuthUserBanned:             "账号已被封禁",

	ErrOAuthInvalidClient:       "无效的客户端",
	ErrOAuthInvalidRedirectURI:  "无效的回调地址",
	ErrOAuthInvalidCode:         "无效的授权码",
	ErrOAuthInvalidCodeVerifier: "无效的代码验证器",
	ErrOAuthInvalidGrant:        "无效的授权类型",
	ErrOAuthInvalidScope:        "无效的权限范围",
	ErrOAuthAccessDenied:        "访问被拒绝",
	ErrOAuthInvalidClientSecret: "客户端密钥无效",
	ErrOAuthPKCERequired:        "公开客户端必须使用 PKCE",
	ErrOAuthConsentRequired:     "需要用户授权同意",

	// Moemoepoint
	ErrMoemoepointInvalidDelta:  "萌萌点变动值不能为 0",
	ErrMoemoepointInvalidReason: "未知的萌萌点变动原因",
	ErrMoemoepointIdemConflict:  "幂等键已存在但请求内容不一致",

	ErrGalgameNotFound:           "Galgame 不存在",
	ErrGalgameAlreadyExists:      "Galgame 已存在",
	ErrGalgameInvalidVNDB:        "无效的 VNDB ID",
	ErrGalgameVNDBExists:         "该 VNDB ID 的 Galgame 已存在",
	ErrGalgameForbidden:          "无权操作此 Galgame",
	ErrGalgameClaimNotDraft:      "草稿不可认领",
	ErrGalgameSubmitterOnly:      "仅提交者可编辑",
	ErrGalgameDraftStatusInvalid: "草稿仅可在待审/已拒状态编辑",
	ErrGalgameQuotaExceeded:      "今日投稿配额已用尽",

	ErrArtifactNotFound:   "资源不存在",
	ErrArtifactInvalid:    "无效的资源",
	ErrArtifactVirusFound: "检测到病毒文件",
	ErrArtifactTooBig:     "文件过大",
	ErrArtifactProcessing: "资源正在处理中",

	ErrModerationPending:  "内容待审核",
	ErrModerationRejected: "内容审核未通过",
	ErrModerationNotFound: "审核任务不存在",

	ErrSiteNotFound:      "站点不存在",
	ErrSiteAlreadyExists: "站点已存在",

	// Image service
	ErrImageUnauthorized:     "未授权访问图片服务",
	ErrImageBadClient:        "无效的客户端 ID",
	ErrImageBadSecret:        "客户端密钥错误",
	ErrImageSiteDisabled:     "该站点未开启图片服务",
	ErrImageSiteUnconfigured: "该站点缺少图片服务配置",
	ErrImagePresetDenied:     "该站点不允许使用此 preset",
	ErrImageFileTooLarge:     "文件大小超过限制",
	ErrImageQuotaExceeded:    "当日配额已用完",
	ErrImageMIMEDenied:       "该 preset 不接受此格式",
	ErrImageDecodeFailed:     "图片解码失败",
	ErrImagePresetNotFound:   "未知的 preset",
	ErrImageStoreFailed:      "图片存储失败",
	ErrImageNotFound:         "图片不存在",
	ErrImageBadRequest:       "请求格式错误",
	ErrImageUploadDisabled:   "图片上传功能暂未开放，敬请期待",
}

// GetMessage returns the message for an error code
func GetMessage(code int) string {
	if msg, ok := codeMessages[code]; ok {
		return msg
	}
	return "未知错误"
}
