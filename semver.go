package main

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"

	"golang.org/x/sync/semaphore"
)

var semverSemaphore = semaphore.NewWeighted(64)

// RunSemver executes the semver command with given arguments and returns the output
func RunSemver(args ...string) (string, error) {
	err := semverSemaphore.Acquire(context.Background(), 1)
	defer semverSemaphore.Release(1)

	if err != nil {
		return "", fmt.Errorf("semver semaphore error: %v", err)
	}

	cmd := exec.Command("node", append([]string{"./node_modules/semver/bin/semver.js"}, args...)...)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err = cmd.Run()
	if err != nil {
		return "", fmt.Errorf("semver error: %v\nstdout: %s\nstderr: %s",
			err, stdout.String(), stderr.String())
	}
	return strings.TrimSpace(stdout.String()), nil
}

// IsValidSemver returns true if the version string can be coerced into a valid semver
func IsValidSemver(version string) bool {
	_, err := RunSemver("-c", version)
	return err == nil
}

// GetMatchingVersions returns all versions that match the given version string
func GetMatchingVersions(version string, versions []string) ([]string, error) {
	// Always try version range matching first
	versionArgs := []string{"-r", version}
	versionArgs = append(versionArgs, versions...)
	matchingVersions, err := RunSemver(versionArgs...)

	// If it fails, it could be a dist tag or invalid version
	// Let the caller handle the error and check for dist tags
	if err != nil {
		return []string{}, err
	}

	return strings.Split(matchingVersions, "\n"), nil
}
