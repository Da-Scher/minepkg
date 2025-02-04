package manifest

import "strings"

// InterpretedDependency is a key-value dependency that has been interpreted.
// It can help to fetch the dependency more easily
type InterpretedDependency struct {
	// Provider is the system that should be used to fetch this dependency.
	// This usually is `minepkg` and can also be `https`. There might be more providers in the future
	Provider string
	// Name is the name of the package
	Name string
	// Source is what `Provider` will need to fetch the given Dependency
	// In practice this is a version number for `Provider === "minepkg"` and
	// a https url for `Provider === "https"`
	Source string
	// IsDev is true if this is a dev dependency
	IsDev bool
}

// InterpretedDependencies returns the dependencies in a `[]*InterpretedDependency` slice.
// See `InterpretedDependency` for details
func (m *Manifest) InterpretedDependencies() []*InterpretedDependency {
	interpreted := make([]*InterpretedDependency, len(m.Dependencies))

	i := 0
	for name, source := range m.Dependencies {
		interpreted[i] = interpretSingleDependency(name, source)
		i++
	}

	return interpreted
}

// InterpretedDevDependencies returns the dev.dependencies in a `[]*InterpretedDependency` slice.
// See `InterpretedDependency` for details
func (m *Manifest) InterpretedDevDependencies() []*InterpretedDependency {
	interpreted := make([]*InterpretedDependency, len(m.Dev.Dependencies))

	i := 0
	for name, source := range m.Dev.Dependencies {
		interpreted[i] = interpretSingleDependency(name, source)
		interpreted[i].IsDev = true
		i++
	}

	return interpreted
}

func interpretSingleDependency(name string, source string) *InterpretedDependency {
	switch {
	case strings.HasPrefix(source, "https://"):
		return &InterpretedDependency{Name: name, Provider: "https", Source: source}
	case source == "none":
		return &InterpretedDependency{Name: name, Provider: "dummy", Source: "none"}
	default:
		sourceParts := strings.SplitN(source, ":", 2)
		if len(sourceParts) == 1 {
			return &InterpretedDependency{Name: name, Provider: "minepkg", Source: source}
		}

		return &InterpretedDependency{Name: name, Provider: sourceParts[0], Source: sourceParts[1]}
	}
}
