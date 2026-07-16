// Package route is the semantic-route dispatch table — the extensibility point
// of the gateway. v0 registers ONE route (moderate-text); adding a future route
// (moderate-image / summarize / chat / search) is one entry here plus its
// handler + DTO. The metering and budget paths key off the route NAME and read
// FailOpen from this table, so they are route-agnostic and need no change when
// a route is added (doc 20 §4, charter ruling 2).
package route

import "api/internal/platform/ai/model"

// Spec is a route's dispatch metadata. FailOpen fixes the failure posture by
// route TYPE (moderation routes fail-open → allow + warn; functional routes,
// added later, fail-closed → degrade the feature), never by the caller.
type Spec struct {
	Name     string
	FailOpen bool
}

// registry is the config table. To add a route: add a row (and its handler/DTO).
var registry = map[string]Spec{
	model.RouteModerateText: {Name: model.RouteModerateText, FailOpen: true},
}

// Lookup returns the spec for a route name and whether it is registered.
func Lookup(name string) (Spec, bool) {
	s, ok := registry[name]
	return s, ok
}
