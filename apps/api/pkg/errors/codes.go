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
	ErrAuthCodeInvalid      = 10010
	ErrAuthCodeExpired      = 10011

	// OAuth (15000-15999)
	ErrOAuthInvalidClient       = 15001
	ErrOAuthInvalidRedirectURI  = 15002
	ErrOAuthInvalidCode         = 15003
	ErrOAuthInvalidCodeVerifier = 15004
	ErrOAuthInvalidGrant        = 15005
	ErrOAuthInvalidScope        = 15006
	ErrOAuthAccessDenied        = 15007

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
	ErrAuthCodeInvalid:      "验证码无效",
	ErrAuthCodeExpired:      "验证码已过期",

	ErrOAuthInvalidClient:       "无效的客户端",
	ErrOAuthInvalidRedirectURI:  "无效的回调地址",
	ErrOAuthInvalidCode:         "无效的授权码",
	ErrOAuthInvalidCodeVerifier: "无效的代码验证器",
	ErrOAuthInvalidGrant:        "无效的授权类型",
	ErrOAuthInvalidScope:        "无效的权限范围",
	ErrOAuthAccessDenied:        "访问被拒绝",

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
}

// GetMessage returns the message for an error code
func GetMessage(code int) string {
	if msg, ok := codeMessages[code]; ok {
		return msg
	}
	return "未知错误"
}
