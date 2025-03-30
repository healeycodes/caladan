package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"

	"golang.org/x/sync/errgroup"
	"golang.org/x/sync/semaphore"
)

type PackageResolver struct {
	resolved     map[string]PackageInfo
	resolvedLock sync.RWMutex
	client       *http.Client
	semaphore    *semaphore.Weighted
}

func NewPackageResolver(client *http.Client, httpSemaphore *semaphore.Weighted) *PackageResolver {
	return &PackageResolver{
		resolved:  make(map[string]PackageInfo),
		client:    client,
		semaphore: httpSemaphore,
	}
}

func (r *PackageResolver) collectPeerDependencies(
	ctx context.Context,
	dependencies []PackageInfo,
) ([]PackageInfo, error) {
	var peerDepsLock sync.Mutex

	g, ctx := errgroup.WithContext(ctx)

	// Add peer dependencies to top level
	for _, dep := range dependencies {
		dep := dep // capture loop variable
		g.Go(func() error {
			resolved, err := r.ResolveDependency(ctx, dep.Name, dep.Version)
			if err != nil {
				return err
			}

			peerDepsLock.Lock()
			for name, version := range resolved.PeerDependencies {
				// Only warn about unmet peer dependencies
				isDirectDep := false
				for _, directDep := range dependencies {
					if directDep.Name == name {
						isDirectDep = true
						break
					}
				}

				if !isDirectDep {
					fmt.Printf("Warning: Package %s has unmet peer dependency %s@%s\n",
						dep.Name, name, version)
				}
			}
			peerDepsLock.Unlock()

			return nil
		})
	}

	if err := g.Wait(); err != nil {
		return nil, err
	}

	// Return original dependencies without automatically adding peer deps
	result := make([]PackageInfo, len(dependencies))
	copy(result, dependencies)
	return result, nil
}

func (r *PackageResolver) ResolveDependencies(
	ctx context.Context,
	dependencies []PackageInfo,
) ([]PackageInfo, error) {
	// First collect all peer dependencies
	dependencies, err := r.collectPeerDependencies(ctx, dependencies)
	if err != nil {
		return nil, err
	}

	// Continue with normal resolution
	g, ctx := errgroup.WithContext(ctx)
	resolvedDeps := make([]PackageInfo, len(dependencies))

	for i, dep := range dependencies {
		i, dep := i, dep // capture loop variables
		g.Go(func() error {
			resolved, err := r.ResolveDependency(ctx, dep.Name, dep.Version)
			if err != nil {
				return err
			}
			resolvedDeps[i] = resolved
			return nil
		})
	}

	if err := g.Wait(); err != nil {
		return nil, err
	}
	return resolvedDeps, nil
}

func (r *PackageResolver) ResolveDependency(
	ctx context.Context,
	name string,
	version string,
) (PackageInfo, error) {
	// First check if we've already resolved any version of this package
	r.resolvedLock.RLock()
	nameWithAt := name + "@"
	for existingKey, existingPkg := range r.resolved {
		if strings.HasPrefix(existingKey, nameWithAt) {
			// Check if existing version satisfies our requirement
			matches, err := GetMatchingVersions(version, []string{existingPkg.Version})
			if err == nil && len(matches) > 0 {
				r.resolvedLock.RUnlock()
				return existingPkg, nil
			}
		}
	}
	r.resolvedLock.RUnlock()

	// No compatible version found, continue with normal resolution...
	uniqueKey := name + "@" + version

	if err := r.semaphore.Acquire(ctx, 1); err != nil {
		return PackageInfo{}, err
	}
	defer r.semaphore.Release(1)

	// Resolve package metadata first (we need this for both paths)
	metadata, err := resolvePackageMetadata(ctx, r.client, name, version)
	if err != nil {
		return PackageInfo{}, err
	}

	// Get all available versions
	keys := make([]string, len(metadata.Versions))
	i := 0
	for k := range metadata.Versions {
		keys[i] = k
		i++
	}

	// Try to match as semver range first
	_, err = GetMatchingVersions(version, keys)
	if err != nil {
		// If semver matching failed, check if it's a dist tag
		if tagVersion, ok := metadata.DistTags[version]; ok {
			fmt.Printf("Using '%s' tag for %s: %s\n", version, name, tagVersion)
			version = tagVersion
		} else {
			// Not a valid version or known tag
			fmt.Printf("Warning: Tag '%s' for package '%s' doesn't exist\n", version, name)
			return PackageInfo{}, fmt.Errorf("'%s' is not a valid version or tag", version)
		}
	}

	// Find exact version
	pkgInfo, err := latestMatchingVersion(version, metadata)
	if err != nil {
		return PackageInfo{}, err
	}

	// Collect all dependencies
	allDeps := make(map[string]string)
	for k, v := range pkgInfo.Dependencies {
		allDeps[k] = v
	}

	// Resolve dependencies concurrently using errgroup
	g, gctx := errgroup.WithContext(ctx)
	resolvedDeps := make(map[string]PackageInfo)
	var resolvedLock sync.Mutex

	for depName, depVersion := range allDeps {
		depName, depVersion := depName, depVersion // capture loop variables
		g.Go(func() error {
			depPkg, err := r.ResolveDependency(gctx, depName, depVersion)
			if err != nil {
				return fmt.Errorf("failed to resolve %s@%s: %v", depName, depVersion, err)
			}

			resolvedLock.Lock()
			resolvedDeps[depName] = depPkg
			resolvedLock.Unlock()
			return nil
		})
	}

	if err := g.Wait(); err != nil {
		return PackageInfo{}, err
	}

	// Update package info
	pkgInfo.Dependencies = make(map[string]string)
	pkgInfo.DevDependencies = make(map[string]string)
	pkgInfo.ResolvedDeps = resolvedDeps
	for depName, depPkg := range resolvedDeps {
		pkgInfo.Dependencies[depName] = depPkg.Version
	}

	// Cache result with write lock
	r.resolvedLock.Lock()
	r.resolved[uniqueKey] = pkgInfo
	r.resolvedLock.Unlock()
	return pkgInfo, nil
}

func HoistDependencies(dependencies []PackageInfo) []PackageInfo {
	// Track all unique packages by name@version
	packages := make(map[string]PackageInfo)
	counts := make(map[string]int)

	// Recursively collect all packages and their counts
	var collectPackages func(deps []PackageInfo, level int)
	collectPackages = func(deps []PackageInfo, level int) {
		for _, dep := range deps {
			key := dep.Name + "@" + dep.Version
			packages[key] = dep
			counts[key]++

			// Process nested dependencies
			if len(dep.ResolvedDeps) > 0 {
				nested := make([]PackageInfo, 0, len(dep.ResolvedDeps))
				for _, pkg := range dep.ResolvedDeps {
					nested = append(nested, pkg)
				}
				collectPackages(nested, level+1)
			}
		}
	}
	collectPackages(dependencies, 0)

	// Start with direct dependencies
	hoisted := make([]PackageInfo, len(dependencies))
	copy(hoisted, dependencies)

	// Track what's at the root level
	rootPackages := make(map[string]string) // name -> version
	for _, dep := range hoisted {
		rootPackages[dep.Name] = dep.Version
	}

	// Try to hoist packages that appear multiple times
	for key, count := range counts {
		if count <= 1 {
			continue
		}

		pkg := packages[key]
		name, version := pkg.Name, pkg.Version

		// Check if we can hoist to root
		if existingVersion, exists := rootPackages[name]; !exists || existingVersion == version {
			// No conflict at root, can be hoisted
			if !exists {
				rootPackages[name] = version
				hoisted = append(hoisted, pkg)
			}

			// Update all references to use the hoisted version
			var updateRefs func(deps []PackageInfo)
			updateRefs = func(deps []PackageInfo) {
				for i := range deps {
					// Clean direct dependencies
					cleanDeps := make(map[string]PackageInfo)
					for depName, depInfo := range deps[i].ResolvedDeps {
						if depInfo.Name == name && depInfo.Version == version {
							// Skip this dep as it's now hoisted
							continue
						}
						cleanDeps[depName] = depInfo
						// Recursively update nested deps
						updateRefs([]PackageInfo{depInfo})
					}
					deps[i].ResolvedDeps = cleanDeps
				}
			}
			updateRefs(hoisted)
		}
	}

	return hoisted
}

func GenerateLockFile(dependencies []PackageInfo) (string, error) {
	lockfile := struct {
		LockfileVersion int                    `json:"lockfileVersion"`
		Requires        bool                   `json:"requires"`
		Packages        map[string]PackageInfo `json:"packages"`
	}{
		LockfileVersion: 3,
		Requires:        true,
		Packages:        make(map[string]PackageInfo),
	}

	// Add root package
	lockfile.Packages[""] = PackageInfo{
		Dependencies: func() map[string]string {
			deps := make(map[string]string)
			for _, d := range dependencies {
				if d.Name != "" && d.Version != "" {
					deps[d.Name] = d.Version
				}
			}
			return deps
		}(),
	}

	seen := make(map[string]bool)
	var addPackage func(pkg PackageInfo, path string) error
	addPackage = func(pkg PackageInfo, path string) error {
		if pkg.Name == "" || pkg.Version == "" {
			return fmt.Errorf("invalid package: missing name or version")
		}

		if !seen[path] {
			seen[path] = true
			lockfile.Packages[path] = pkg

			for _, dep := range pkg.ResolvedDeps {
				newPath := "node_modules/" + dep.Name
				if path != "" {
					newPath = path + "/node_modules/" + dep.Name
				}
				if err := addPackage(dep, newPath); err != nil {
					return err
				}
			}
		}
		return nil
	}

	for _, dep := range dependencies {
		if err := addPackage(dep, "node_modules/"+dep.Name); err != nil {
			return "", err
		}
	}

	out, err := json.MarshalIndent(lockfile, "", "  ")
	if err != nil {
		return "", fmt.Errorf("failed to generate lockfile JSON: %v", err)
	}

	return string(out), nil
}

func resolvePackageMetadata(ctx context.Context, client *http.Client, dep string, version string) (*PackageMetadata, error) {
	fmt.Printf("Resolving package metadata for %s@%s\n", dep, version)

	registryURL := fmt.Sprintf("https://registry.npmjs.org/%s", dep)
	req, err := http.NewRequestWithContext(ctx, "GET", registryURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %v", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch package metadata: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("npm registry returned status %d", resp.StatusCode)
	}

	var metadata PackageMetadata
	if err := json.NewDecoder(resp.Body).Decode(&metadata); err != nil {
		return nil, fmt.Errorf("failed to parse package metadata: %v", err)
	}

	return &metadata, nil
}

func latestMatchingVersion(version string, metadata *PackageMetadata) (PackageInfo, error) {
	keys := make([]string, len(metadata.Versions))
	i := 0
	for k := range metadata.Versions {
		keys[i] = k
		i++
	}

	matches, err := GetMatchingVersions(version, keys)
	if err != nil {
		return PackageInfo{}, err
	}
	if len(matches) == 0 {
		return PackageInfo{}, fmt.Errorf("no matching versions found for %s", version)
	}

	// Get the package info for the latest matching version
	pkgInfo := metadata.Versions[matches[len(matches)-1]]

	// Verify required dist information
	if pkgInfo.Dist.Tarball == "" {
		return PackageInfo{}, fmt.Errorf("missing tarball URL in package metadata")
	}
	if pkgInfo.Dist.Integrity == "" {
		return PackageInfo{}, fmt.Errorf("missing integrity hash in package metadata")
	}

	// Copy dist information
	pkgInfo.Resolved = pkgInfo.Dist.Tarball
	pkgInfo.Integrity = pkgInfo.Dist.Integrity

	return pkgInfo, nil
}
