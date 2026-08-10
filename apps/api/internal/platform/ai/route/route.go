package route

import "api/internal/platform/ai/model"

type Spec struct {
	Name     string
	FailOpen bool
}

var registry = map[string]Spec{
	model.RouteModerateText: {Name: model.RouteModerateText, FailOpen: true},
}

func Lookup(name string) (Spec, bool) {
	s, ok := registry[name]
	return s, ok
}
