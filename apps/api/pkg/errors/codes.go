package errors

// 错误码按模块分段
// 10000-19999: Auth
// 20000-29999: Game
// 30000-39999: Content
// 40000-49999: Comment
// 50000-59999: Artifact
// 60000-69999: Moderation
// 70000-79999: Site

const (
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

	// Game (20000-29999)
	ErrGameNotFound         = 20001
	ErrGameAlreadyExists    = 20002
	ErrGameRevisionConflict = 20003
	ErrGameTagNotFound      = 20004

	// Content (30000-39999)
	ErrContentNotFound  = 30001
	ErrContentForbidden = 30002

	// Comment (40000-49999)
	ErrCommentNotFound  = 40001
	ErrCommentForbidden = 40002

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
	ErrAuthUnauthorized:     "unauthorized",
	ErrAuthInvalidToken:     "invalid token",
	ErrAuthTokenExpired:     "token expired",
	ErrAuthInvalidPassword:  "invalid password",
	ErrAuthUserNotFound:     "user not found",
	ErrAuthEmailExists:      "email already exists",
	ErrAuthNameExists:       "username already exists",
	ErrAuthPasswordRequired: "password reset required",
	ErrAuthInvalidEmail:     "invalid email format",
	ErrAuthCodeInvalid:      "invalid verification code",
	ErrAuthCodeExpired:      "verification code expired",

	ErrGameNotFound:         "game not found",
	ErrGameAlreadyExists:    "game already exists",
	ErrGameRevisionConflict: "game revision conflict",
	ErrGameTagNotFound:      "game tag not found",

	ErrContentNotFound:  "content not found",
	ErrContentForbidden: "content access forbidden",

	ErrCommentNotFound:  "comment not found",
	ErrCommentForbidden: "comment access forbidden",

	ErrArtifactNotFound:   "artifact not found",
	ErrArtifactInvalid:    "invalid artifact",
	ErrArtifactVirusFound: "virus detected in artifact",
	ErrArtifactTooBig:     "artifact too large",
	ErrArtifactProcessing: "artifact is being processed",

	ErrModerationPending:  "content pending moderation",
	ErrModerationRejected: "content rejected by moderation",
	ErrModerationNotFound: "moderation job not found",

	ErrSiteNotFound:      "site not found",
	ErrSiteAlreadyExists: "site already exists",
}

// GetMessage returns the message for an error code
func GetMessage(code int) string {
	if msg, ok := codeMessages[code]; ok {
		return msg
	}
	return "unknown error"
}
