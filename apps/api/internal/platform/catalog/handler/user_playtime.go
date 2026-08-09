// user_playtime.go — the playtime report face: the door a third-party Galgame
// manager knocks on after its user signs in with a NextMoe account.
//
// It is a FOURTH face, on its own prefix, and the two differences from
// /api/v1/user/catalog are both deliberate:
//
//  1. Its own scopes (playtime:read / playtime:write) rather than
//     catalog:edit. A launcher that wants to record how long you played has no
//     business holding the right to rewrite a work's metadata, and a consent
//     screen that asks for one while meaning the other teaches users to stop
//     reading consent screens.
//
//  2. NO catalog-site binding requirement. The catalog write planes resolve a
//     tenant from oauth_clients.catalog_site because every row they write is
//     attributed to a product site. A playtime report is attributed to a USER
//     and a CLIENT; demanding a catalog tenant would shut out precisely the
//     third-party apps this face exists for. The client must still be
//     registered — a revoked client's token writes nothing.
//
// The prefix is disjoint from /api/v1/user/catalog for the same reason that
// one is disjoint from /api/v1/catalog: Huma registers on the Fiber app, so a
// path-scoped Use is the only gate, and an overlapping prefix would put the
// wrong auth chain in front of these routes.
package handler

import (
	"context"
	stderrors "errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"api/internal/platform/catalog/dto"
	"api/internal/platform/catalog/service"
	siteModel "api/internal/platform/site/model"
	"api/pkg/errors"
	"api/pkg/response"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humafiber"
	"github.com/gofiber/fiber/v3"
)

// The playtime scopes. Split read from write because the two callers are
// different: a Galgame manager needs both (it reports, and it syncs back on a
// new device), while a site that only wants to show "you played 30h" beside a
// rating form needs read alone — and should not be able to write.
const (
	ScopePlaytimeRead  = "playtime:read"
	ScopePlaytimeWrite = "playtime:write"
)

// PlaytimePrefix is the mount point of the playtime face.
const PlaytimePrefix = "/api/v1/user/playtime"

// playtimeMinePageSize caps one sync page. A 300-game library is four pages;
// the cap exists so a client cannot ask for the whole table in one query.
const playtimeMinePageSize = 200

const ctxKeyScope ctxKey = "catalog:token_scope"

// PlaytimeGate is the authorization half of the playtime chain, applied after
// middleware.JWTAuth. It enforces the three things every op here needs — a
// user, a registered client, and at least ONE playtime scope — and leaves the
// read/write distinction to the ops, which know which of the two they are.
//
// Requiring "at least one" here rather than nothing keeps a token with no
// playtime grant at all out of the face entirely, so an op's own check is the
// second line rather than the only one.
func PlaytimeGate(clients OAuthClientLookup) fiber.Handler {
	return func(c fiber.Ctx) error {
		uid, _ := c.Locals("user_id").(uint)
		if uid == 0 {
			return response.UnauthorizedMsg(c, errors.ErrAuthUnauthorized,
				"the access token carries no user identity")
		}

		scope, _ := c.Locals("user_scope").(string)
		if !hasScope(scope, ScopePlaytimeRead) && !hasScope(scope, ScopePlaytimeWrite) {
			return response.ForbiddenMsg(c, errors.ErrForbidden,
				"the access token is missing the "+ScopePlaytimeRead+" / "+ScopePlaytimeWrite+" scope")
		}

		// The client id is the report's third key member, so a token that is
		// not client-bound cannot report: there would be nothing to attribute
		// the measurement to, and nothing to exclude if it misbehaved.
		clientID, _ := c.Locals("token_client_id").(string)
		if clientID == "" {
			return response.ForbiddenMsg(c, errors.ErrForbidden,
				"the access token is not bound to an OAuth client; the playtime face requires a client-bound token")
		}
		client, err := clients.FindByClientID(c.Context(), clientID)
		if err != nil || client == nil {
			return response.ForbiddenMsg(c, errors.ErrForbidden,
				"the access token's client is not registered")
		}

		// Deliberately NOT checked: client.CatalogSite. See the file header.
		c.Locals(localClient, client)
		return c.Next()
	}
}

// PlaytimeBridge lifts the token's uid, its client and its scope string into
// the Huma context. The scope rides along because the per-op check needs it
// and the gate only proved that ONE of the two is present.
func PlaytimeBridge(ctx huma.Context, next func(huma.Context)) {
	fc := humafiber.Unwrap(ctx)
	if id, ok := fc.Locals("user_id").(uint); ok {
		ctx = huma.WithValue(ctx, ctxKeyUserID, int64(id))
	}
	if scope, ok := fc.Locals("user_scope").(string); ok {
		ctx = huma.WithValue(ctx, ctxKeyScope, scope)
	}
	if client, ok := fc.Locals(localClient).(*siteModel.OAuthClient); ok {
		ctx = huma.WithValue(ctx, ctxKeyClient, client)
	}
	next(ctx)
}

// PlaytimeServer holds the face's single dependency.
type PlaytimeServer struct{ svc *service.UserPlaytimeService }

// SetupPlaytime builds the playtime Huma API. Auth is installed by the caller
// as path-scoped Fiber middleware on PlaytimePrefix. Callable with a nil
// service for spec export.
func SetupPlaytime(app *fiber.App, svc *service.UserPlaytimeService) huma.API {
	InstallErrorEnvelope()

	cfg := huma.DefaultConfig("KUN Playtime API", "1.0.0")
	cfg.OpenAPIPath = ""
	cfg.DocsPath = ""
	cfg.SchemasPath = ""

	api := humafiber.New(app, cfg)
	api.UseMiddleware(PlaytimeBridge)
	RegisterPlaytimeOps(api, svc)
	return api
}

// RegisterPlaytimeOps registers the playtime operations. SetupPlaytime calls
// it for the runtime face; cmd/gen-openapi calls it on the exported catalog
// contract so downstream app authors read this face beside the ones it sits
// next to.
func RegisterPlaytimeOps(api huma.API, svc *service.UserPlaytimeService) {
	s := &PlaytimeServer{svc: svc}
	tags := []string{"playtime"}

	huma.Register(api, huma.Operation{
		OperationID: "reportPlaytime", Method: http.MethodPut,
		Path:    PlaytimePrefix + "/works/{workID}",
		Summary: "Report the bearer token's own playtime on a work. The body carries the ABSOLUTE cumulative total in minutes, never a delta — re-sending the same number is a no-op, which makes the call safe to retry. Keyed by (user, work, client): a second app of the same user reports alongside, not over. Requires playtime:write",
		Tags:    tags,
	}, s.report)

	huma.Register(api, huma.Operation{
		OperationID: "reportPlaytimeByRef", Method: http.MethodPut,
		Path:    PlaytimePrefix + "/by-ref/{source}/{externalID}",
		Summary: "Report playtime addressing the work by an external id the client already holds (vndb/dlsite/getchu/bangumi …) instead of a catalog work id. Only EXACT anchors resolve; the response echoes the resolved work_id, which the client should cache. 404 when nothing is anchored to that id. Requires playtime:write",
		Tags:    tags,
	}, s.reportByRef)

	huma.Register(api, huma.Operation{
		OperationID: "reportPlaytimeBatch", Method: http.MethodPost,
		Path:    PlaytimePrefix + "/batch",
		Summary: "Report up to 200 works in one call — the first-login library sync. Each item is accepted or rejected on its own and the response reports per-item outcomes; a single bad item never fails the batch. Requires playtime:write",
		Tags:    tags,
	}, s.reportBatch)

	huma.Register(api, huma.Operation{
		OperationID: "listOwnPlaytime", Method: http.MethodGet,
		Path:    PlaytimePrefix + "/mine",
		Summary: "Page the bearer token's own playtime rows in (updated_at) order — the sync-back leg for a second device. Hand `cursor` back as ?updated_since= to fetch only what changed. Requires playtime:read",
		Tags:    tags,
	}, s.listMine)

	huma.Register(api, huma.Operation{
		OperationID: "getOwnPlaytimeForWork", Method: http.MethodGet,
		Path:    PlaytimePrefix + "/works/{workID}",
		Summary: "The bearer token's own playtime on ONE work, folded across their applications (MAX minutes — two apps watching one save file are not two playthroughs). `playtime` is null when the user has never reported here; that is a 200, not a 404. This is the call a rating form makes to offer 'you played 30h — attach it?'. Requires playtime:read",
		Tags:    tags,
	}, s.getMine)
}

// requireScope is the per-op half of the scope check. The gate proved the
// token carries one of the two; this proves it carries the RIGHT one.
func requireScope(ctx context.Context, want string) *houseError {
	scope, _ := ctx.Value(ctxKeyScope).(string)
	if !hasScope(scope, want) {
		return apiErrMsg(http.StatusForbidden, errors.ErrForbidden,
			"the access token is missing the "+want+" scope")
	}
	return nil
}

// playtimeActor resolves who is reporting and from which application. Unlike
// the catalog write faces there is no site to demand — an unbound client
// reports with an empty site string, which is honest provenance rather than a
// missing value.
func playtimeActor(ctx context.Context) (uid int64, clientID, site string, he *houseError) {
	uid = userIDFromCtx(ctx)
	if uid <= 0 {
		return 0, "", "", apiErrMsg(http.StatusUnauthorized, errors.ErrAuthUnauthorized,
			"the access token carries no user identity")
	}
	client := clientFromCtx(ctx)
	if client == nil || client.ID == "" {
		return 0, "", "", apiErrMsg(http.StatusForbidden, errors.ErrForbidden,
			"the access token is not bound to a registered OAuth client")
	}
	return uid, client.ID, client.CatalogSite, nil
}

// playtimeErr maps the service refusals onto the house envelope.
func playtimeErr(err error) error {
	switch {
	case stderrors.Is(err, service.ErrPlaytimeMinutesRange),
		stderrors.Is(err, service.ErrPlaytimeBadStatus),
		stderrors.Is(err, service.ErrPlaytimeUnknownSource):
		return apiErrMsg(http.StatusBadRequest, errors.ErrInvalidParam, err.Error())
	case stderrors.Is(err, service.ErrPlaytimeActorRequired),
		stderrors.Is(err, service.ErrPlaytimeClientRequired):
		return apiErrMsg(http.StatusForbidden, errors.ErrForbidden, err.Error())
	case stderrors.Is(err, service.ErrPlaytimeWorkUnavailable),
		stderrors.Is(err, service.ErrPlaytimeRefUnresolved):
		return apiErrMsg(http.StatusNotFound, errors.ErrNotFound, err.Error())
	}
	slog.Error("catalog playtime", "err", err)
	return apiErr(http.StatusInternalServerError, errors.ErrInternalServer)
}

// --- write legs ---

type playtimeReportInput struct {
	WorkID int64 `path:"workID" minimum:"1" doc:"Catalog work id"`
	Body   dto.PlaytimeReportBody
}

type playtimeRecordOutput struct {
	Body Envelope[dto.PlaytimeRecordResponse]
}

func (s *PlaytimeServer) report(ctx context.Context, in *playtimeReportInput) (*playtimeRecordOutput, error) {
	if he := requireScope(ctx, ScopePlaytimeWrite); he != nil {
		return nil, he
	}
	uid, clientID, site, he := playtimeActor(ctx)
	if he != nil {
		return nil, he
	}
	rec, err := s.store(ctx, uid, clientID, site, in.WorkID, in.Body)
	if err != nil {
		return nil, err
	}
	return &playtimeRecordOutput{Body: okEnvelope(*rec)}, nil
}

type playtimeByRefInput struct {
	Source     string `path:"source" doc:"External source key (vndb, dlsite, getchu, bangumi, …)"`
	ExternalID string `path:"externalID" doc:"The id this game carries on that source (e.g. v17, RJ01234)"`
	Body       dto.PlaytimeReportBody
}

func (s *PlaytimeServer) reportByRef(ctx context.Context, in *playtimeByRefInput) (*playtimeRecordOutput, error) {
	if he := requireScope(ctx, ScopePlaytimeWrite); he != nil {
		return nil, he
	}
	uid, clientID, site, he := playtimeActor(ctx)
	if he != nil {
		return nil, he
	}
	workID, err := s.svc.ResolveRef(ctx, in.Source, in.ExternalID)
	if err != nil {
		return nil, playtimeErr(err)
	}
	rec, err := s.store(ctx, uid, clientID, site, workID, in.Body)
	if err != nil {
		return nil, err
	}
	rec.ResolvedFrom = in.Source + ":" + in.ExternalID
	return &playtimeRecordOutput{Body: okEnvelope(*rec)}, nil
}

// store is the shared tail of both single-write legs: decode the wire values,
// hand them to the service, render the stored row.
func (s *PlaytimeServer) store(ctx context.Context, uid int64, clientID, site string,
	workID int64, body dto.PlaytimeReportBody) (*dto.PlaytimeRecordResponse, error) {

	status, lastPlayed, he := decodeReportBody(body)
	if he != nil {
		return nil, he
	}
	rec, err := s.svc.Report(ctx, service.PlaytimeReport{
		ActorUID: uid, WorkID: workID, ClientID: clientID, Site: site,
		Minutes: body.Minutes, Status: status, LastPlayedAt: lastPlayed,
	})
	if err != nil {
		return nil, playtimeErr(err)
	}
	out := renderPlaytimeRecord(*rec)
	return &out, nil
}

// decodeReportBody turns the wire's status word and timestamp into stored
// forms. An omitted status is `playing` (see the DTO); an unparseable
// timestamp is the caller's error, not a silent null.
func decodeReportBody(body dto.PlaytimeReportBody) (int16, *time.Time, *houseError) {
	status, ok := dto.PlaytimeStatusFromWord(orDefault(body.Status, dto.PlaytimeStatusWordPlaying))
	if !ok {
		return 0, nil, apiErrMsg(http.StatusBadRequest, errors.ErrInvalidParam,
			"unknown playtime status "+body.Status)
	}
	var lastPlayed *time.Time
	if body.LastPlayedAt != nil && *body.LastPlayedAt != "" {
		t, err := time.Parse(time.RFC3339, *body.LastPlayedAt)
		if err != nil {
			return 0, nil, apiErrMsg(http.StatusBadRequest, errors.ErrInvalidParam,
				"last_played_at must be an RFC 3339 timestamp")
		}
		lastPlayed = &t
	}
	return status, lastPlayed, nil
}

type playtimeBatchInput struct {
	Body dto.PlaytimeBatchBody
}

type playtimeBatchOutput struct {
	Body Envelope[dto.PlaytimeBatchResponse]
}

// reportBatch is the first-login library sync. It walks the items in order and
// records an outcome for each; only an infrastructure failure aborts the whole
// call. A manager with 300 games where 40 are not in the catalog wants the 260
// stored and a list of the 40 — not a 404 for the lot.
func (s *PlaytimeServer) reportBatch(ctx context.Context, in *playtimeBatchInput) (*playtimeBatchOutput, error) {
	if he := requireScope(ctx, ScopePlaytimeWrite); he != nil {
		return nil, he
	}
	uid, clientID, site, he := playtimeActor(ctx)
	if he != nil {
		return nil, he
	}

	out := dto.PlaytimeBatchResponse{Results: make([]dto.PlaytimeBatchResult, 0, len(in.Body.Items))}
	for i, item := range in.Body.Items {
		res := dto.PlaytimeBatchResult{Index: i}
		workID, err := s.resolveBatchTarget(ctx, item)
		if err != nil {
			res.Status, res.Error = classifyBatchErr(err), err.Error()
			out.Refused++
			out.Results = append(out.Results, res)
			continue
		}
		status, lastPlayed, he := decodeReportBody(dto.PlaytimeReportBody{
			Minutes: item.Minutes, Status: item.Status, LastPlayedAt: item.LastPlayedAt,
		})
		if he != nil {
			res.Status, res.Error, res.WorkID = "rejected", he.Message, workID
			out.Refused++
			out.Results = append(out.Results, res)
			continue
		}
		if _, err := s.svc.Report(ctx, service.PlaytimeReport{
			ActorUID: uid, WorkID: workID, ClientID: clientID, Site: site,
			Minutes: item.Minutes, Status: status, LastPlayedAt: lastPlayed,
		}); err != nil {
			res.Status, res.Error, res.WorkID = classifyBatchErr(err), err.Error(), workID
			out.Refused++
			out.Results = append(out.Results, res)
			continue
		}
		res.Status, res.WorkID = "ok", workID
		out.Accepted++
		out.Results = append(out.Results, res)
	}
	return &playtimeBatchOutput{Body: okEnvelope(out)}, nil
}

// resolveBatchTarget applies the one-of rule: a work id addresses directly, a
// source+external_id pair resolves, and neither (or a half-filled pair) is the
// caller's error.
func (s *PlaytimeServer) resolveBatchTarget(ctx context.Context, item dto.PlaytimeBatchItem) (int64, error) {
	if item.WorkID > 0 {
		return item.WorkID, nil
	}
	if item.Source == "" || item.ExternalID == "" {
		return 0, stderrors.New("each item needs work_id, or both source and external_id")
	}
	return s.svc.ResolveRef(ctx, item.Source, item.ExternalID)
}

// classifyBatchErr sorts a per-item failure into the two words a client acts
// on differently: not_found means "your library has a game we do not carry"
// (show it, offer to submit it), rejected means "fix your values".
func classifyBatchErr(err error) string {
	if stderrors.Is(err, service.ErrPlaytimeWorkUnavailable) ||
		stderrors.Is(err, service.ErrPlaytimeRefUnresolved) ||
		stderrors.Is(err, service.ErrPlaytimeUnknownSource) {
		return "not_found"
	}
	return "rejected"
}

// --- read legs ---

type playtimeMineInput struct {
	UpdatedSince string `query:"updated_since" doc:"RFC 3339 timestamp; returns only rows changed AFTER it. Omit for a full first pull"`
	Limit        int    `query:"limit" minimum:"1" maximum:"200" default:"200" doc:"Page size"`
}

type playtimeMineOutput struct {
	Body Envelope[dto.PlaytimeMineResponse]
}

func (s *PlaytimeServer) listMine(ctx context.Context, in *playtimeMineInput) (*playtimeMineOutput, error) {
	if he := requireScope(ctx, ScopePlaytimeRead); he != nil {
		return nil, he
	}
	uid := userIDFromCtx(ctx)
	var since time.Time
	if in.UpdatedSince != "" {
		t, err := time.Parse(time.RFC3339, in.UpdatedSince)
		if err != nil {
			return nil, apiErrMsg(http.StatusBadRequest, errors.ErrInvalidParam,
				"updated_since must be an RFC 3339 timestamp")
		}
		since = t
	}
	limit := in.Limit
	if limit <= 0 || limit > playtimeMinePageSize {
		limit = playtimeMinePageSize
	}
	rows, err := s.svc.ListMine(ctx, uid, since, limit)
	if err != nil {
		return nil, playtimeErr(err)
	}
	out := dto.PlaytimeMineResponse{Items: make([]dto.PlaytimeRecordResponse, 0, len(rows))}
	for _, r := range rows {
		out.Items = append(out.Items, renderPlaytimeRecord(r))
	}
	// The cursor is the last row's updated_at. An empty page leaves it null,
	// which tells the client it is caught up rather than to poll a timestamp
	// that will never advance on its own.
	if n := len(rows); n > 0 {
		cursor := rows[n-1].UpdatedAt.UTC().Format(time.RFC3339Nano)
		out.Cursor = &cursor
	}
	return &playtimeMineOutput{Body: okEnvelope(out)}, nil
}

type playtimeSelfInput struct {
	WorkID int64 `path:"workID" minimum:"1" doc:"Catalog work id"`
}

type playtimeSelfOutput struct {
	Body Envelope[*dto.PlaytimeSelfResponse]
}

func (s *PlaytimeServer) getMine(ctx context.Context, in *playtimeSelfInput) (*playtimeSelfOutput, error) {
	if he := requireScope(ctx, ScopePlaytimeRead); he != nil {
		return nil, he
	}
	uid := userIDFromCtx(ctx)
	got, err := s.svc.GetMine(ctx, uid, in.WorkID)
	if err != nil {
		return nil, playtimeErr(err)
	}
	if got == nil {
		return &playtimeSelfOutput{Body: okEnvelope[*dto.PlaytimeSelfResponse](nil)}, nil
	}
	return &playtimeSelfOutput{Body: okEnvelope(&dto.PlaytimeSelfResponse{
		WorkID:       got.WorkID,
		Minutes:      got.Minutes,
		Status:       dto.PlaytimeStatusWord(got.Status),
		LastPlayedAt: renderTimePtr(got.LastPlayedAt),
		Clients:      got.Clients,
	})}, nil
}

// --- rendering ---

func renderPlaytimeRecord(r service.PlaytimeRecord) dto.PlaytimeRecordResponse {
	return dto.PlaytimeRecordResponse{
		WorkID:       r.WorkID,
		Minutes:      r.Minutes,
		Status:       dto.PlaytimeStatusWord(r.Status),
		LastPlayedAt: renderTimePtr(r.LastPlayedAt),
		ClientID:     r.ClientID,
		UpdatedAt:    r.UpdatedAt.UTC().Format(time.RFC3339Nano),
	}
}

func renderTimePtr(t *time.Time) *string {
	if t == nil {
		return nil
	}
	s := t.UTC().Format(time.RFC3339Nano)
	return &s
}

func orDefault(s, fallback string) string {
	if strings.TrimSpace(s) == "" {
		return fallback
	}
	return s
}
