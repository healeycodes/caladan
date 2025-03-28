package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
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

func (r *PackageResolver) ResolveDependencies(
	ctx context.Context,
	dependencies []PackageInfo,
) ([]PackageInfo, error) {
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
	uniqueKey := fmt.Sprintf("%s@%s", name, version)

	// Check cache first with read lock
	r.resolvedLock.RLock()
	if pkg, ok := r.resolved[uniqueKey]; ok {
		r.resolvedLock.RUnlock()
		return pkg, nil
	}
	r.resolvedLock.RUnlock()

	if err := r.semaphore.Acquire(ctx, 1); err != nil {
		return PackageInfo{}, err
	}
	defer r.semaphore.Release(1)

	// Resolve package metadata
	metadata, err := resolvePackageMetadata(ctx, r.client, name, version)
	if err != nil {
		return PackageInfo{}, err
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
	for depName, depPkg := range resolvedDeps {
		pkgInfo.Dependencies[depName] = depPkg.Version
	}

	// Cache result with write lock
	r.resolvedLock.Lock()
	r.resolved[uniqueKey] = pkgInfo
	r.resolvedLock.Unlock()
	return pkgInfo, nil
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
	err, versions := GetMatchingVersions(version, keys)
	if err != nil {
		return PackageInfo{}, err
	}

	return metadata.Versions[versions[len(versions)-1]], nil
}
