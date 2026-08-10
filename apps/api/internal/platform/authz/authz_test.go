package authz_test

import (
	"testing"

	"api/internal/platform/authz"
)

const (
	postRead    authz.Permission = "post.read"
	postEdit    authz.Permission = "post.edit"
	postPublish authz.Permission = "post.publish"
	postUnknown authz.Permission = "post.nonexistent"
)

func newTestResolver() *authz.Resolver {
	return authz.NewResolver(authz.Bundles{
		"viewer": {postRead},
		"editor": {postRead, postEdit},
		"admin":  {postRead, postEdit, postPublish},
	})
}

func TestResolverCan(t *testing.T) {
	r := newTestResolver()

	cases := []struct {
		name  string
		roles []string
		perm  authz.Permission
		want  bool
	}{
		{"single role hit", []string{"editor"}, postEdit, true},
		{"single role miss", []string{"viewer"}, postEdit, false},
		{"nil roles", nil, postRead, false},
		{"empty roles", []string{}, postRead, false},
		{"unknown role", []string{"ghost"}, postRead, false},
		{"unknown permission", []string{"admin"}, postUnknown, false},
		{"union across roles grants", []string{"viewer", "admin"}, postPublish, true},
		{"union with unknown role still grants", []string{"ghost", "editor"}, postEdit, true},
		{"none of several roles grant", []string{"viewer", "ghost"}, postPublish, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := r.Can(tc.roles, tc.perm); got != tc.want {
				t.Errorf("Can(%v, %q) = %v, want %v", tc.roles, tc.perm, got, tc.want)
			}
		})
	}
}

func TestResolverIsolatedFromSourceBundles(t *testing.T) {
	b := authz.Bundles{"editor": {postEdit}}
	r := authz.NewResolver(b)
	b["editor"] = append(b["editor"], postPublish)
	b["admin"] = []authz.Permission{postPublish}

	if r.Can([]string{"editor"}, postPublish) {
		t.Error("resolver reflected a post-construction bundle mutation (editor)")
	}
	if r.Can([]string{"admin"}, postPublish) {
		t.Error("resolver reflected a post-construction bundle mutation (new role)")
	}
}
