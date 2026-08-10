package dto

type CoverVoteResponse struct {
	CoverID   int64 `json:"cover_id"`
	VoteCount int64 `json:"vote_count" doc:"the cover's advisory vote total after this write"`
	Voted     bool  `json:"voted" doc:"the acting user's resulting state on this cover"`
}

type UserWorkCover struct {
	ID        int64  `json:"id" doc:"catalog_work_cover row id (the vote endpoints' cover id)"`
	ImageHash string `json:"image_hash"`
	VoteCount int    `json:"vote_count" doc:"advisory best-cover votes on this cover"`
	Voted     bool   `json:"voted" doc:"true if the token user's ballot is on this cover"`
}

type UserWorkCoversResponse struct {
	WorkID int64           `json:"work_id"`
	Covers []UserWorkCover `json:"covers"`
}
