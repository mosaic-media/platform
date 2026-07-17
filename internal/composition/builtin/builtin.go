package builtin

// Manifest is a built-in module's declaration of the Platform contracts it
// fulfills. It mirrors the manifest a future external Module (MEG-006) would
// publish, reduced to what this slice needs: an identity, a version and the
// list of fulfilled contract names.
type Manifest struct {
	ID       string
	Version  string
	Fulfills []string
}

// Module is a compiled-in, required, trusted module. Discovery is by
// registration rather than by scanning a filesystem, but the shape — a
// module that presents a Manifest — matches how an external Module would be
// discovered at runtime.
type Module interface {
	Manifest() Manifest
}

// Registry holds registered built-in modules in registration order. The
// composition root builds one, registers each built-in module, and can then
// inspect the declared manifests before wiring concrete contracts.
type Registry struct {
	modules []Module
}

// NewRegistry returns an empty Registry.
func NewRegistry() *Registry {
	return &Registry{}
}

// Register adds a module to the registry.
func (r *Registry) Register(m Module) {
	r.modules = append(r.modules, m)
}

// Modules returns the registered modules in registration order.
func (r *Registry) Modules() []Module {
	return append([]Module(nil), r.modules...)
}

// Manifests returns the manifest of every registered module.
func (r *Registry) Manifests() []Manifest {
	manifests := make([]Manifest, 0, len(r.modules))
	for _, m := range r.modules {
		manifests = append(manifests, m.Manifest())
	}
	return manifests
}
