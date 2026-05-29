package repository

import (
	"context"
	"strings"

	"api/internal/platform/auth/model"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// userSortColumns is the allowlist of sortable columns for FindAllPaginated.
// Any value outside this set falls back to created_at, so an unvalidated /
// attacker-controlled sort_by can never reach ORDER BY.
var userSortColumns = map[string]string{
	"created_at":  "created_at",
	"name":        "name",
	"email":       "email",
	"moemoepoint": "moemoepoint",
}

// UserRepository handles user data access
type UserRepository struct {
	db *gorm.DB
}

// NewUserRepository creates a new UserRepository
func NewUserRepository(db *gorm.DB) *UserRepository {
	return &UserRepository{db: db}
}

// FindByID finds a user by ID
func (r *UserRepository) FindByID(ctx context.Context, id uint) (*model.User, error) {
	var user model.User
	if err := r.db.WithContext(ctx).First(&user, id).Error; err != nil {
		return nil, err
	}
	return &user, nil
}

// FindByUUID finds a user by UUID
func (r *UserRepository) FindByUUID(ctx context.Context, uuid string) (*model.User, error) {
	var user model.User
	if err := r.db.WithContext(ctx).Where("uuid = ?", uuid).First(&user).Error; err != nil {
		return nil, err
	}
	return &user, nil
}

// FindByUUIDWithRoles finds a user by UUID and preloads their roles
func (r *UserRepository) FindByUUIDWithRoles(ctx context.Context, uuid string) (*model.User, error) {
	var user model.User
	if err := r.db.WithContext(ctx).Preload("Roles").Where("uuid = ?", uuid).First(&user).Error; err != nil {
		return nil, err
	}
	return &user, nil
}

// FindByIDWithRoles finds a user by ID and preloads their roles
func (r *UserRepository) FindByIDWithRoles(ctx context.Context, id uint) (*model.User, error) {
	var user model.User
	if err := r.db.WithContext(ctx).Preload("Roles").First(&user, id).Error; err != nil {
		return nil, err
	}
	return &user, nil
}

// SearchByName returns up to `limit` users whose name matches `query`
// (case-insensitive substring) with roles preloaded.
//
// Results are ranked in-database: exact match > prefix match > substring
// match, then alphabetical within each tier. Pushing the rank into the
// CASE in ORDER BY (rather than re-ranking in Go after over-fetching)
// matters here because the candidate set can be huge — for a one-char
// query like "鲲" against 70k+ users, the exact match would otherwise be
// buried far past any reasonable over-fetch window.
//
// LIKE wildcards (`%`, `_`, `\`) in the query are escaped so user input
// like "foo_" or "50%" matches literally.
func (r *UserRepository) SearchByName(ctx context.Context, query string, limit int) ([]model.User, error) {
	escaped := escapeLikePattern(query)
	substring := "%" + escaped + "%"
	prefix := escaped + "%"

	var users []model.User
	err := r.db.WithContext(ctx).
		Preload("Roles").
		Where("name ILIKE ?", substring).
		Clauses(clause.OrderBy{
			Expression: clause.Expr{
				SQL: "CASE " +
					"WHEN LOWER(name) = LOWER(?) THEN 0 " +
					"WHEN name ILIKE ? THEN 1 " +
					"ELSE 2 END, name ASC",
				Vars: []any{query, prefix},
			},
		}).
		Limit(limit).
		Find(&users).Error
	if err != nil {
		return nil, err
	}
	return users, nil
}

// escapeLikePattern escapes the three characters PostgreSQL's LIKE/ILIKE
// treat specially: backslash (the default escape char), percent (any-run
// wildcard), and underscore (single-char wildcard).
//
// Backslash MUST be replaced first; otherwise the inserted backslashes
// from the % and _ rules would themselves get doubled.
func escapeLikePattern(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `%`, `\%`)
	s = strings.ReplaceAll(s, `_`, `\_`)
	return s
}

// FindByIDsWithRoles batch-fetches users by IDs, preloading roles. Returned
// slice is in arbitrary order; missing IDs are simply not in the result.
// Caller should compute the diff to identify not-found ids.
func (r *UserRepository) FindByIDsWithRoles(ctx context.Context, ids []uint) ([]model.User, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	var users []model.User
	if err := r.db.WithContext(ctx).
		Preload("Roles").
		Where("id IN ?", ids).
		Find(&users).Error; err != nil {
		return nil, err
	}
	return users, nil
}

// FindByEmail finds a user by email
func (r *UserRepository) FindByEmail(ctx context.Context, email string) (*model.User, error) {
	var user model.User
	if err := r.db.WithContext(ctx).Where("email = ?", email).First(&user).Error; err != nil {
		return nil, err
	}
	return &user, nil
}

// FindByName finds a user by name
func (r *UserRepository) FindByName(ctx context.Context, name string) (*model.User, error) {
	var user model.User
	if err := r.db.WithContext(ctx).Where("name = ?", name).First(&user).Error; err != nil {
		return nil, err
	}
	return &user, nil
}

// Create creates a new user
func (r *UserRepository) Create(ctx context.Context, user *model.User) error {
	return r.db.WithContext(ctx).Create(user).Error
}

// Update updates a user
func (r *UserRepository) Update(ctx context.Context, user *model.User) error {
	return r.db.WithContext(ctx).Save(user).Error
}

// CountByAvatarHash counts users (other than excludeID) referencing the given
// avatar_image_hash. Used before GC-ing an avatar on anonymize so we never
// delete a hash another user still points at (image_service dedups content
// and has no refcount of its own).
func (r *UserRepository) CountByAvatarHash(ctx context.Context, hash string, excludeID uint) (int64, error) {
	var n int64
	err := r.db.WithContext(ctx).
		Model(&model.User{}).
		Where("avatar_image_hash = ? AND id <> ?", hash, excludeID).
		Count(&n).Error
	return n, err
}

// UpdatePassword updates a user's password
func (r *UserRepository) UpdatePassword(ctx context.Context, uuid string, password string) error {
	return r.db.WithContext(ctx).Model(&model.User{}).Where("uuid = ?", uuid).Update("password", password).Error
}

// MigrateLegacyPassword sets the new password and clears legacy password fields
func (r *UserRepository) MigrateLegacyPassword(ctx context.Context, userID uint, newPasswordHash string) error {
	return r.db.WithContext(ctx).Model(&model.User{}).Where("id = ?", userID).Updates(map[string]any{
		"password":        newPasswordHash,
		"kungal_password": nil,
		"moyu_password":   nil,
	}).Error
}

// UpdateEmail updates a user's email
func (r *UserRepository) UpdateEmail(ctx context.Context, uuid string, email string) error {
	return r.db.WithContext(ctx).Model(&model.User{}).Where("uuid = ?", uuid).Update("email", email).Error
}

// UpdateProfile sets the provided fields on the user with the given uuid.
// Only fields present in `fields` are touched; this is a partial update,
// not a full overwrite.
//
// Caller is responsible for uniqueness validation on `name` (the service
// layer does this via ExistsByNameExcluding before calling) — this
// repository method is mechanical.
func (r *UserRepository) UpdateProfile(ctx context.Context, uuid string, fields map[string]any) error {
	if len(fields) == 0 {
		return nil
	}
	return r.db.WithContext(ctx).
		Model(&model.User{}).
		Where("uuid = ?", uuid).
		Updates(fields).Error
}

// Delete soft deletes a user
func (r *UserRepository) Delete(ctx context.Context, id uint) error {
	return r.db.WithContext(ctx).Delete(&model.User{}, id).Error
}

// List lists users with pagination
func (r *UserRepository) List(ctx context.Context, offset, limit int) ([]model.User, int64, error) {
	var users []model.User
	var total int64

	if err := r.db.WithContext(ctx).Model(&model.User{}).Count(&total).Error; err != nil {
		return nil, 0, err
	}

	if err := r.db.WithContext(ctx).Offset(offset).Limit(limit).Find(&users).Error; err != nil {
		return nil, 0, err
	}

	return users, total, nil
}

// ExistsByEmail checks if a user exists by email
func (r *UserRepository) ExistsByEmail(ctx context.Context, email string) (bool, error) {
	var count int64
	if err := r.db.WithContext(ctx).Model(&model.User{}).Where("email = ?", email).Count(&count).Error; err != nil {
		return false, err
	}
	return count > 0, nil
}

// ExistsByName checks if a user exists by name
func (r *UserRepository) ExistsByName(ctx context.Context, name string) (bool, error) {
	var count int64
	if err := r.db.WithContext(ctx).Model(&model.User{}).Where("name = ?", name).Count(&count).Error; err != nil {
		return false, err
	}
	return count > 0, nil
}

// ExistsByEmailExcluding checks if email exists excluding a specific user
func (r *UserRepository) ExistsByEmailExcluding(ctx context.Context, email, excludeUUID string) (bool, error) {
	var count int64
	if err := r.db.WithContext(ctx).Model(&model.User{}).
		Where("email = ? AND uuid != ?", email, excludeUUID).
		Count(&count).Error; err != nil {
		return false, err
	}
	return count > 0, nil
}

// ExistsByNameExcluding checks if name exists excluding a specific user
func (r *UserRepository) ExistsByNameExcluding(ctx context.Context, name, excludeUUID string) (bool, error) {
	var count int64
	if err := r.db.WithContext(ctx).Model(&model.User{}).
		Where("name = ? AND uuid != ?", name, excludeUUID).
		Count(&count).Error; err != nil {
		return false, err
	}
	return count > 0, nil
}

// FindAllPaginated finds users with pagination, search, and sorting
func (r *UserRepository) FindAllPaginated(
	ctx context.Context,
	page, limit int,
	search string,
	status *int,
	sortBy string,
	sortDesc bool,
) ([]model.User, int64, error) {
	var users []model.User
	var total int64

	query := r.db.WithContext(ctx).Model(&model.User{})

	// Apply search filter
	if search != "" {
		query = query.Where("name ILIKE ? OR email ILIKE ?", "%"+search+"%", "%"+search+"%")
	}

	// Apply status filter
	if status != nil {
		query = query.Where("status = ?", *status)
	}

	// Count total
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	// Apply sorting. Whitelist the column and use a structured ORDER BY
	// clause (GORM identifier-quotes it) so an attacker-controlled sort_by
	// can never be interpolated raw into the ORDER BY — defense in depth even
	// if the handler-level validator is ever bypassed.
	col, ok := userSortColumns[sortBy]
	if !ok {
		col = "created_at"
	}
	query = query.Order(clause.OrderByColumn{
		Column: clause.Column{Name: col},
		Desc:   sortDesc,
	})

	// Apply pagination
	offset := (page - 1) * limit
	if err := query.Offset(offset).Limit(limit).Find(&users).Error; err != nil {
		return nil, 0, err
	}

	return users, total, nil
}
