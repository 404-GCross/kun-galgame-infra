// Package permissions is the cross-domain permission console: the one place
// that knows ALL live permission vocabularies at once, the runtime overlay that
// can widen them without a deploy, and the admin face that renders and edits
// the resulting matrix.
//
// It sits ABOVE the per-domain perm packages: each of those owns its own keys
// and code bundles and knows nothing about the others (that separation is the
// point of docs/auth/04 §2.2). Aggregating them is a console concern, so the
// aggregation lives here — a leaf package that imports the six live perm
// packages and is imported by nothing they depend on, so no cycle is possible.
//
// The RETIRED galgame/perm package is deliberately absent. It has zero live
// enforcement points, so listing its keys would invite an operator to grant a
// permission that no running code ever checks.
package permissions

import (
	aiPerm "api/internal/platform/ai/perm"
	artifactPerm "api/internal/platform/artifact/perm"
	"api/internal/platform/authz"
	catalogPerm "api/internal/platform/catalog/perm"
	devapiPerm "api/internal/platform/devapi/perm"
	sitePerm "api/internal/platform/site/perm"
	trustPerm "api/internal/platform/trust/perm"
)

// Role names of the five-role contract (docs/integration/oauth/11-roles.md).
// They are the matrix's columns, in contract order.
const (
	RoleUser      = "user"
	RoleCreator   = "creator"
	RoleModerator = "moderator"
	RoleAdmin     = "admin"
	RoleRen       = "ren"
)

// MatrixRoles is the column order of the console matrix.
var MatrixRoles = []string{RoleUser, RoleCreator, RoleModerator, RoleAdmin, RoleRen}

// axisRank ranks the MANAGEMENT axis (ren > admin > moderator). creator is a
// qualification role, not a management tier, and `user` is implicit — both rank
// 0, and neither is ever a delegation target reachable by rank comparison
// (creator has its own explicit ren-only rule; user rows are immutable).
var axisRank = map[string]int{
	RoleModerator: 1,
	RoleAdmin:     2,
	RoleRen:       3,
}

// HighestRank returns the caller's position on the management axis: 0 for a
// caller holding none of moderator/admin/ren.
func HighestRank(roles []string) int {
	best := 0
	for _, r := range roles {
		if rank := axisRank[r]; rank > best {
			best = rank
		}
	}
	return best
}

// Key is one permission plus the human-facing text the console renders. The
// descriptions live here rather than in each perm package so the whole matrix
// reads in one voice and one file — and TestRegistryCoversEveryBundledKey pins
// that no key can be added to a bundle without also being described here.
type Key struct {
	Permission authz.Permission
	DescEN     string
	DescZH     string
}

// Domain is one perm package as the console sees it: its vocabulary, its code
// bundles (the floor), the Holder whose Resolver is swapped when the overlay
// changes, and the keys it forbids the overlay to grant.
type Domain struct {
	Name         string
	TitleZH      string
	Bundles      authz.Bundles
	Holder       *authz.Holder
	NonDelegable authz.NonDelegable
	Keys         []Key
}

// Registry is the immutable set of live domains, indexed by permission.
type Registry struct {
	domains []Domain
	byKey   map[authz.Permission]int
}

// NewRegistry indexes the domains. Exported so tests can build a small registry
// instead of the live one.
func NewRegistry(domains ...Domain) *Registry {
	r := &Registry{domains: domains, byKey: make(map[authz.Permission]int)}
	for i, d := range domains {
		for _, k := range d.Keys {
			r.byKey[k.Permission] = i
		}
	}
	return r
}

// Domains returns the registered domains in console order.
func (r *Registry) Domains() []Domain { return r.domains }

// Lookup finds the domain owning p. ok=false means p is not a live key — the
// validator's first rejection, which is what keeps a typo'd or retired key out
// of the overlay table.
func (r *Registry) Lookup(p authz.Permission) (Domain, bool) {
	i, ok := r.byKey[p]
	if !ok {
		return Domain{}, false
	}
	return r.domains[i], true
}

// IsNonDelegable reports whether p may never be granted through the overlay.
// An unknown key answers true — fail-closed, though the validator rejects it
// earlier for not existing.
func (r *Registry) IsNonDelegable(p authz.Permission) bool {
	d, ok := r.Lookup(p)
	if !ok {
		return true
	}
	return d.NonDelegable.Has(p)
}

// Effective reports whether role currently holds p, code bundles ∪ overlay —
// it asks the domain's live Holder, which IS the merged state.
func (r *Registry) Effective(role string, p authz.Permission) bool {
	d, ok := r.Lookup(p)
	if !ok {
		return false
	}
	return d.Holder.Can([]string{role}, p)
}

// InCodeBundle reports whether p is granted to role by the COMPILED-IN table —
// the floor the overlay may widen but never cut.
func (r *Registry) InCodeBundle(role string, p authz.Permission) bool {
	d, ok := r.Lookup(p)
	if !ok {
		return false
	}
	for _, granted := range d.Bundles[role] {
		if granted == p {
			return true
		}
	}
	return false
}

// live is the registry of the six domains that have live enforcement points.
var live = NewRegistry(
	Domain{
		Name:         "oauth",
		TitleZH:      "IdP 控制台",
		Bundles:      sitePerm.Bundles,
		Holder:       sitePerm.Resolver,
		NonDelegable: sitePerm.NonDelegable,
		Keys: []Key{
			{sitePerm.AdminAccess, "Reach the admin console at all (the page/list gate).", "进入管理控制台(页面与列表门)"},
			{sitePerm.UsersPIIView, "See user PII — email in the list, email and IP in the detail.", "查看用户 PII(列表邮箱、详情邮箱与 IP)"},
			{sitePerm.RolesGrantBasic, "Grant and revoke the below-admin roles (moderator, creator).", "授予/撤销 admin 以下角色(moderator、creator)"},
			{sitePerm.RolesGrantSite, "Grant and revoke site-scoped roles.", "授予/撤销站点作用域角色"},
			{sitePerm.RolesGrantAdmin, "Grant and revoke admin.", "授予/撤销 admin 角色"},
			{sitePerm.SitesCreate, "Create a site.", "创建站点"},
			{sitePerm.SitesUpdate, "Update a site.", "编辑站点"},
			{sitePerm.SitesDelete, "Delete a site.", "删除站点"},
			{sitePerm.SitesManageAll, "See and edit every site and client, not only self-created rows.", "跨创建者管理全部站点与客户端(而非仅自建行)"},
			{sitePerm.ClientsCreate, "Create an OAuth client.", "创建 OAuth 客户端"},
			{sitePerm.ClientsUpdate, "Update an OAuth client.", "编辑 OAuth 客户端"},
			{sitePerm.ClientsDelete, "Delete an OAuth client.", "删除 OAuth 客户端"},
			{sitePerm.ClientsStorageConfig, "Enable a client's object-storage capabilities (artifact / image).", "开启客户端存储能力(artifact / image)"},
			{sitePerm.ClientsPrivilegedConfig, "Set the sensitive client fields (ren-only scopes, auto_consent, display_order).", "设置敏感客户端字段(ren 专属 scope、auto_consent、display_order)"},
			{sitePerm.PermissionsManage, "Run the permission console — grant and revoke overlay rows.", "运行权限控制台(授予/撤销叠加行)"},
		},
	},
	Domain{
		Name:    "catalog",
		TitleZH: "目录注册表",
		Bundles: catalogPerm.Bundles,
		Holder:  catalogPerm.Resolver,
		Keys: []Key{
			{catalogPerm.Review, "Reach the catalog identity-registry curation surface (merge / unmerge / reconcile).", "目录注册表策展面(合并/拆分/对账)"},
			{catalogPerm.ClaimReview, "Decide a product site's submission — approve / decline / ban / unban.", "裁决产品站投稿认领(通过/驳回/封禁/解封)"},
			{catalogPerm.EditWork, "Propose an edit on catalog.work through the editing engine.", "对 catalog.work 提交编辑提案"},
			{catalogPerm.EditWorkReview, "Adjudicate a catalog.work proposal (amend / merge / decline / revert).", "裁决 catalog.work 提案(修订/合入/驳回/回滚)"},
			{catalogPerm.EditTaxonomy, "Propose an edit on the shared vocabulary (label / tag / engine / series).", "对共享词表提交编辑提案(label/tag/engine/series)"},
			{catalogPerm.EditTaxonomyReview, "Adjudicate a vocabulary proposal.", "裁决词表提案"},
		},
	},
	Domain{
		Name:    "trust",
		TitleZH: "Trust & Safety",
		Bundles: trustPerm.Bundles,
		Holder:  trustPerm.Resolver,
		Keys: []Key{
			{trustPerm.QueueAccess, "Reach the T&S review inbox (list / claim / decide, registries, dead letters).", "进入 T&S 审核收件箱(队列/登记表/死信)"},
			{trustPerm.TermManage, "Create and deprecate Tier0 word-list terms.", "增改/退役 Tier0 词表词条"},
		},
	},
	Domain{
		Name:    "ai",
		TitleZH: "AI 网关",
		Bundles: aiPerm.Bundles,
		Holder:  aiPerm.Resolver,
		Keys: []Key{
			{aiPerm.UsageView, "Reach the AI-gateway usage / cost / budget dashboard.", "查看 AI 网关用量/成本/预算看板"},
		},
	},
	Domain{
		Name:    "devapi",
		TitleZH: "开发者平台",
		Bundles: devapiPerm.Bundles,
		Holder:  devapiPerm.Resolver,
		Keys: []Key{
			{devapiPerm.Manage, "Manage open-API applications — tier / quota, and mint / rotate / revoke keys.", "管理开放 API 应用(tier/配额,铸造/轮换/吊销 key)"},
		},
	},
	Domain{
		Name:    "artifact",
		TitleZH: "文件存储",
		Bundles: artifactPerm.Bundles,
		Holder:  artifactPerm.Resolver,
		Keys: []Key{
			{artifactPerm.FilesManage, "Browse, delete and reclaim stored artifact files.", "浏览/删除/回收 artifact 文件"},
		},
	},
)

// Live returns the registry of the six live domains.
func Live() *Registry { return live }
