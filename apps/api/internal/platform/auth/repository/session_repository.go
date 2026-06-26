package repository

import (
	"context"
	"time"

	"api/internal/platform/auth/model"

	"gorm.io/gorm"
)

// SessionRepository handles session data access
type SessionRepository struct {
	db *gorm.DB
}

// NewSessionRepository creates a new SessionRepository
func NewSessionRepository(db *gorm.DB) *SessionRepository {
	return &SessionRepository{db: db}
}

// FindBySessionToken finds a session by session token
func (r *SessionRepository) FindBySessionToken(ctx context.Context, token string) (*model.Session, error) {
	var session model.Session
	if err := r.db.WithContext(ctx).Where("session_token = ?", token).First(&session).Error; err != nil {
		return nil, err
	}
	return &session, nil
}

// FindByRefreshToken finds a session by its CURRENT refresh token only.
func (r *SessionRepository) FindByRefreshToken(ctx context.Context, token string) (*model.Session, error) {
	var session model.Session
	if err := r.db.WithContext(ctx).Where("refresh_token = ?", token).First(&session).Error; err != nil {
		return nil, err
	}
	return &session, nil
}

// FindByRefreshTokenOrPrev finds a session whose CURRENT or PREVIOUS
// refresh token matches. The caller compares the token against
// session.RefreshToken to tell a normal rotation from a grace-window
// replay. Powers refresh-token rotation with a grace window.
func (r *SessionRepository) FindByRefreshTokenOrPrev(ctx context.Context, token string) (*model.Session, error) {
	var session model.Session
	if err := r.db.WithContext(ctx).
		Where("refresh_token = ? OR prev_refresh_token = ?", token, token).
		First(&session).Error; err != nil {
		return nil, err
	}
	return &session, nil
}

// FindByID loads a session by primary key. Used to re-read a session after a
// lost rotation race (see RotateRefreshToken) so the caller can converge on the
// winning rotation's current token instead of a token the DB never stored.
func (r *SessionRepository) FindByID(ctx context.Context, id uint) (*model.Session, error) {
	var session model.Session
	if err := r.db.WithContext(ctx).First(&session, id).Error; err != nil {
		return nil, err
	}
	return &session, nil
}

// RotateRefreshToken atomically rotates a session's tokens, but ONLY if its
// current refresh_token still equals oldRefresh (compare-and-swap). It demotes
// oldRefresh to prev_refresh_token and installs the new access/refresh tokens.
// Returns won=true when THIS call performed the rotation (1 row matched), or
// won=false when a concurrent refresh already rotated (0 rows) — the caller
// then converges on the winner's current token.
//
// This is the fix for the lost-update race: two concurrent refreshes that read
// the same current token would otherwise both Save divergent new tokens, so the
// cookie and DB end up holding different tokens → the next request 401s with a
// token that is neither current nor prev. The CAS lets exactly one win. Only
// the rotation columns are written (like TouchLastUsed) so it can't clobber a
// concurrent single-column update on the same row.
func (r *SessionRepository) RotateRefreshToken(ctx context.Context, id uint, oldRefresh, newAccess, newRefresh string, rotatedAt, expiresAt time.Time) (bool, error) {
	res := r.db.WithContext(ctx).
		Model(&model.Session{}).
		Where("id = ? AND refresh_token = ?", id, oldRefresh).
		Updates(map[string]any{
			"session_token":      newAccess,
			"prev_refresh_token": oldRefresh,
			"refresh_token":      newRefresh,
			"rotated_at":         rotatedAt,
			"expires_at":         expiresAt,
		})
	if res.Error != nil {
		return false, res.Error
	}
	return res.RowsAffected == 1, nil
}

// FindByUserID finds sessions by user ID
func (r *SessionRepository) FindByUserID(ctx context.Context, userID uint) ([]model.Session, error) {
	var sessions []model.Session
	if err := r.db.WithContext(ctx).Where("user_id = ?", userID).Find(&sessions).Error; err != nil {
		return nil, err
	}
	return sessions, nil
}

// FindByBrowserID returns the non-expired sessions sharing a browser anchor —
// the multi-account "session bag" (docs/auth/02) — newest activity first.
// Logged-out accounts are hard-deleted, so they naturally drop out.
func (r *SessionRepository) FindByBrowserID(ctx context.Context, browserID string) ([]model.Session, error) {
	var sessions []model.Session
	if browserID == "" {
		return sessions, nil
	}
	if err := r.db.WithContext(ctx).
		Where("browser_id = ? AND expires_at > ?", browserID, time.Now()).
		Order("last_used_at DESC NULLS LAST").
		Find(&sessions).Error; err != nil {
		return nil, err
	}
	return sessions, nil
}

// TouchLastUsed bumps a session's last_used_at as a single-column update, so it
// can't clobber a concurrent token rotation on the same row.
func (r *SessionRepository) TouchLastUsed(ctx context.Context, id uint, t time.Time) error {
	return r.db.WithContext(ctx).Model(&model.Session{}).Where("id = ?", id).Update("last_used_at", t).Error
}

// Create creates a new session
func (r *SessionRepository) Create(ctx context.Context, session *model.Session) error {
	return r.db.WithContext(ctx).Create(session).Error
}

// Update updates a session
func (r *SessionRepository) Update(ctx context.Context, session *model.Session) error {
	return r.db.WithContext(ctx).Save(session).Error
}

// Delete deletes a session
func (r *SessionRepository) Delete(ctx context.Context, id uint) error {
	return r.db.WithContext(ctx).Delete(&model.Session{}, id).Error
}

// DeleteByUserID deletes all sessions for a user
func (r *SessionRepository) DeleteByUserID(ctx context.Context, userID uint) error {
	return r.db.WithContext(ctx).Where("user_id = ?", userID).Delete(&model.Session{}).Error
}

// DeleteByBrowserID removes every session in a browser's bag (logout all).
func (r *SessionRepository) DeleteByBrowserID(ctx context.Context, browserID string) error {
	if browserID == "" {
		return nil
	}
	return r.db.WithContext(ctx).Where("browser_id = ?", browserID).Delete(&model.Session{}).Error
}

// DeleteExpired deletes expired sessions
func (r *SessionRepository) DeleteExpired(ctx context.Context) error {
	return r.db.WithContext(ctx).Where("expires_at < ?", time.Now()).Delete(&model.Session{}).Error
}

// CountByUserID counts sessions for a user
func (r *SessionRepository) CountByUserID(ctx context.Context, userID uint) (int64, error) {
	var count int64
	if err := r.db.WithContext(ctx).Model(&model.Session{}).Where("user_id = ?", userID).Count(&count).Error; err != nil {
		return 0, err
	}
	return count, nil
}
