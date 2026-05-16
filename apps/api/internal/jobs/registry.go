package jobs

import "sort"

// Registry is the set of known jobs, keyed by name. Populate it once at
// process start (see all.go) so "what jobs exist" is auditable in one place.
type Registry struct {
	jobs map[string]Job
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry {
	return &Registry{jobs: make(map[string]Job)}
}

// Register adds a job. A duplicate name overwrites — registration is
// centralized and deterministic, so this only happens on programmer error.
func (r *Registry) Register(j Job) {
	r.jobs[j.Name] = j
}

// Get looks up a job by name.
func (r *Registry) Get(name string) (Job, bool) {
	j, ok := r.jobs[name]
	return j, ok
}

// List returns all jobs sorted by name (stable order for the admin UI).
func (r *Registry) List() []Job {
	out := make([]Job, 0, len(r.jobs))
	for _, j := range r.jobs {
		out = append(out, j)
	}
	sort.Slice(out, func(i, k int) bool { return out[i].Name < out[k].Name })
	return out
}
