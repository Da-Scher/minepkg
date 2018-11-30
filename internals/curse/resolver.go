package curse

import (
	"fmt"
	"sync"

	"github.com/fiws/minepkg/internals/manifest"
)

// Resolver resolves given the mods of given dependencies
type Resolver struct {
	Resolved map[uint32]manifest.ResolvedMod
}

// Resolve find all dependencies from the given `id`
// and adds it to the `resolved` map. Nothing is returned
func (r *Resolver) Resolve(id uint32, version string) {
	var resolve func(id uint32)
	resolve = func(id uint32) {
		_, ok := r.Resolved[id]
		if ok == true {
			return
		}

		modFiles, _ := FetchModFiles(id)
		matchingRelease := FindRelease(modFiles, version)
		if matchingRelease == nil {
			panic(fmt.Sprintf("Mod with id %d does not support mc version %s", id, version))
		}

		r.Resolved[id] = manifest.ResolvedMod{
			DownloadURL: matchingRelease.DownloadURL,
			FileName:    matchingRelease.FileNameOnDisk,
		}
		var wg sync.WaitGroup
		for _, dependency := range matchingRelease.Dependencies {
			if dependency.Type == DependencyTypeRequired {
				wg.Add(1)
				go func(id uint32) {
					defer wg.Done()
					resolve(id)
				}(dependency.AddOnID)
			}
		}
		wg.Wait()
	}

	resolve(id)
}

// NewResolver returns a new resolver
func NewResolver() *Resolver {
	return &Resolver{Resolved: make(map[uint32]manifest.ResolvedMod)}
}
