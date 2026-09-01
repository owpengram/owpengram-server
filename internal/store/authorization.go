package store

import (
	"context"
	"errors"

	"telesrv/internal/domain"
)

var ErrAuthorizationStateChanged = errors.New("authorization state changed")

// AuthorizationStore 持久化设备授权（auth_key ↔ user 绑定）。实现见 store/memory（测试替身）、store/postgres。
type AuthorizationStore interface {
	Bind(ctx context.Context, a domain.Authorization) error
	ByAuthKey(ctx context.Context, authKeyID [8]byte) (domain.Authorization, bool, error)
	// UpdateClientInfo 合并更新已绑定授权的客户端元数据，使设备列表与 auth key 协商事实一致。
	UpdateClientInfo(ctx context.Context, authKeyID [8]byte, info domain.AuthKeyClientInfo) error
	ListByUser(ctx context.Context, userID int64) ([]domain.Authorization, error)
	Delete(ctx context.Context, authKeyID [8]byte) error
	DeleteByHash(ctx context.Context, userID, hash int64) (domain.Authorization, bool, error)
	DeleteByUserExcept(ctx context.Context, userID int64, keepAuthKeyID [8]byte) ([]domain.Authorization, error)
	// MarkPasswordPassed 仅在 auth_key 仍属于 expectedUserID 且仍为
	// password_pending 时提升为完全授权，避免旧用户的密码 proof 提升重绑后的账号。
	MarkPasswordPassed(ctx context.Context, authKeyID [8]byte, expectedUserID int64) error
}

// AuthKeyAuthorityLinker is an optional in-process store-composition boundary.
// Implementations whose auth_keys primary and authorization projection live in
// separate in-memory objects can attach them here so Layer writes update both
// under one state transition. Durable stores normally enforce this inside one
// database transaction and do not implement the hook.
type AuthKeyAuthorityLinker interface {
	LinkAuthKeyAuthority(AuthKeyStore)
}
