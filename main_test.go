package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDownloadPackages(t *testing.T) {
	// Create a temporary directory for the test
	tmpDir, err := os.MkdirTemp("", "npm-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create a simple package info map with a real package
	packages := map[string]PackageInfo{
		"is-odd": {
			Version:   "3.0.1",
			Resolved:  "https://registry.npmjs.org/is-odd/-/is-odd-3.0.1.tgz",
			Integrity: "sha512-CQpnWPrDwmP1+SMHXZhtLtJv90yiyVfluGsX5iNCVkrhQtU3TQHsUWPG9wkdk9Lgd5yNpAg9jQEo90CBaXgWMA==",
		},
	}

	// Download the package
	DownloadPackages(packages, tmpDir)

	// Verify that the package was downloaded and extracted correctly
	expectedFiles := []string{
		"package.json",
		"index.js",
		"LICENSE",
		"README.md",
	}

	for _, file := range expectedFiles {
		path := filepath.Join(tmpDir, "is-odd", file)
		if _, err := os.Stat(path); os.IsNotExist(err) {
			t.Errorf("Expected file %s does not exist", file)
		}
	}
}
