package domain

// PhoneChangeRequest 是账号改号的持久化命令。updateUserPhone 不携带 PTS，
// 因此该命令只维护号码事实；Exclude* 保留在 DTO 中用于兼容调用边界，在线
// 非 PTS 通知由 RPC 层按发起设备排除。
type PhoneChangeRequest struct {
	UserID           int64
	Phone            string
	Date             int
	ExcludeAuthKeyID [8]byte
	ExcludeSessionID int64
	// SignupEmail, when non-empty, is written to users.signup_email in the
	// same transaction as Phone. Only email-signup phone changes set this
	// (see account.Service.ChangePhone); ordinary phone-number changes leave
	// it as the zero value and the column untouched.
	SignupEmail string
}

type PhoneChangeResult struct {
	User    User
	Changed bool
}
