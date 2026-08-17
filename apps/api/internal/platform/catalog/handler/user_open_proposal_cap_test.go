package handler

import (
	"encoding/json"
	"fmt"
	"testing"

	"api/internal/platform/editing"
	"api/pkg/errors"

	"github.com/gofiber/fiber/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

func seedFiledProposals(t *testing.T, db *gorm.DB, uid int64, work int64, n int, site string, status int16) {
	t.Helper()
	if n == 0 {
		return
	}
	rows := make([]editing.Proposal, n)
	for i := range rows {
		rows[i] = editing.Proposal{
			EntityFamily: "catalog",
			EntityType:   "catalog.work",
			EntityID:     work,
			Patch:        datatypes.JSON(`{"catalog.work.display_name":"cap"}`),
			ProposerUID:  uid,
			Site:         site,
			Status:       status,
		}
	}
	require.NoError(t, db.Create(&rows).Error)
}

func countProposalsFor(t *testing.T, db *gorm.DB, uid int64) int64 {
	t.Helper()
	var n int64
	require.NoError(t, db.Raw(`SELECT count(*) FROM edit_proposal WHERE proposer_uid = ?`, uid).Scan(&n).Error)
	return n
}

func TestUserEdit_ThirdPartyOpenProposalCap(t *testing.T) {
	db := openCatalogTestDB(t)
	app := userEditApp(t, db, userEditClients())

	t.Run("the 21st third-party filing is 429 and writes nothing", func(t *testing.T) {
		work := seedUserEditWork(t, db)
		const uid int64 = 870
		seedFiledProposals(t, db, uid, work, 15, "kungal", editing.StatusOpen)
		seedFiledProposals(t, db, uid, work, 5, "letmoe", editing.StatusOpen)
		require.EqualValues(t, 20, countProposalsFor(t, db, uid))

		status, raw := userEditReq(t, app, "POST", UserPrefix+"/edit/proposals",
			userToken(t, uint(uid), ScopeCatalogEdit, "thirdparty-kungal"),
			userProposalBody(work, "第21件", ""))
		assert.Equal(t, fiber.StatusTooManyRequests, status, string(raw))
		var env struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		}
		require.NoError(t, json.Unmarshal(raw, &env), string(raw))
		assert.Equal(t, errors.ErrTooManyRequests, env.Code)
		assert.Contains(t, env.Message, "20")
		assert.Contains(t, env.Message, "withdraw")
		assert.EqualValues(t, 20, countProposalsFor(t, db, uid), "the refusal must not insert a row")
	})

	t.Run("withdrawn merged and declined rows do not count", func(t *testing.T) {
		work := seedUserEditWork(t, db)
		const uid int64 = 871
		seedFiledProposals(t, db, uid, work, 19, "kungal", editing.StatusOpen)
		seedFiledProposals(t, db, uid, work, 1, "kungal", editing.StatusWithdrawn)
		seedFiledProposals(t, db, uid, work, 1, "letmoe", editing.StatusMerged)
		seedFiledProposals(t, db, uid, work, 1, "kungal", editing.StatusDeclined)

		status, raw := userEditReq(t, app, "POST", UserPrefix+"/edit/proposals",
			userToken(t, uint(uid), ScopeCatalogEdit, "thirdparty-kungal"),
			userProposalBody(work, "第20件は通る", ""))
		require.Equal(t, fiber.StatusOK, status, string(raw))
		assert.Equal(t, "open", decodeUserCreate(t, raw).Data.Proposal.Status)

		status, raw = userEditReq(t, app, "POST", UserPrefix+"/edit/proposals",
			userToken(t, uint(uid), ScopeCatalogEdit, "thirdparty-kungal"),
			userProposalBody(work, "第21件は拒否", ""))
		assert.Equal(t, fiber.StatusTooManyRequests, status, string(raw))
		assert.Contains(t, string(raw), "20")
	})

	t.Run("a first-party token is not capped", func(t *testing.T) {
		work := seedUserEditWork(t, db)
		const uid int64 = 872
		seedFiledProposals(t, db, uid, work, 20, "kungal", editing.StatusOpen)

		status, raw := userEditReq(t, app, "POST", UserPrefix+"/edit/proposals",
			userToken(t, uint(uid), ScopeCatalogEdit, "kungal-client"),
			userProposalBody(work, "自社の第21件", ""))
		require.Equal(t, fiber.StatusOK, status, string(raw))
		assert.EqualValues(t, 21, countProposalsFor(t, db, uid))

		status, raw = userEditReq(t, app, "POST", UserPrefix+"/edit/proposals",
			userToken(t, uint(uid), ScopeCatalogEdit, "thirdparty-kungal"),
			userProposalBody(work, "第三者は21件目で止まる", ""))
		assert.Equal(t, fiber.StatusTooManyRequests, status, string(raw),
			"first-party filings count toward the third-party cap")
		assert.EqualValues(t, 21, countProposalsFor(t, db, uid))
	})
}

func TestUserEdit_MissingReferencedTagIs422OnMerge(t *testing.T) {
	db := openCatalogTestDB(t)
	work := seedUserEditWork(t, db)
	app := userEditApp(t, db, userEditClients())

	body := fmt.Sprintf(
		`{"entity_type":"catalog.work","entity_id":%d,"patch":{"catalog.work.tag_ids":[9999999]}}`, work)
	status, raw := userEditReq(t, app, "POST", UserPrefix+"/edit/proposals",
		userToken(t, 880, ScopeCatalogEdit, "kungal-client"), body)
	require.Equal(t, fiber.StatusOK, status, string(raw), "Validate is parse-only; filing must succeed")
	id := decodeUserCreate(t, raw).Data.Proposal.ID
	require.NotZero(t, id)

	status, raw = userEditReq(t, app, "POST", userEditPath(id, "merge"),
		userTokenRoles(t, 881, ScopeCatalogEdit, "kungal-client", "user", "admin"), `{"note":"apply"}`)
	assert.Equal(t, fiber.StatusUnprocessableEntity, status, string(raw))
	assert.NotEqual(t, fiber.StatusInternalServerError, status, string(raw))
	var env struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	}
	require.NoError(t, json.Unmarshal(raw, &env), string(raw))
	assert.Equal(t, errors.ErrValidationFailed, env.Code)
	assert.Contains(t, env.Message, "catalog.work.tag_ids")

	var stillOpen int16
	require.NoError(t, db.Raw(`SELECT status FROM edit_proposal WHERE id = ?`, id).Scan(&stillOpen).Error)
	assert.Equal(t, editing.StatusOpen, stillOpen, "a refused apply must leave the proposal open")

	status, raw = userEditReq(t, app, "POST", userEditPath(id, "decline"),
		userTokenRoles(t, 881, ScopeCatalogEdit, "kungal-client", "user", "admin"), `{"note":"bad tag"}`)
	require.Equal(t, fiber.StatusOK, status, string(raw), "the proposal must not be wedged")
	var closed struct {
		Data struct {
			Status string `json:"status"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(raw, &closed), string(raw))
	assert.Equal(t, "declined", closed.Data.Status)
}
