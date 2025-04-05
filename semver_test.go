package main

import (
	"reflect"
	"testing"
)

func TestIsValidSemver(t *testing.T) {
	tests := []struct {
		name    string
		version string
		want    bool
	}{
		{
			name:    "valid semver",
			version: "1.2.3",
			want:    true,
		},
		{
			name:    "valid semver with prerelease",
			version: "1.2.3-beta.1",
			want:    true,
		},
		{
			name:    "invalid semver",
			version: "not.a.version",
			want:    false,
		},
		{
			name:    "partial version",
			version: "1.2",
			want:    true, // semver will coerce this to 1.2.0
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsValidSemver(tt.version); got != tt.want {
				t.Errorf("IsValidSemver() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestGetMatchingVersions(t *testing.T) {
	availableVersions := []string{"1.0.0", "1.1.0", "1.2.0", "2.0.0", "2.1.0"}

	tests := []struct {
		name     string
		version  string
		versions []string
		want     []string
		wantErr  bool
	}{
		{
			name:     "exact version match",
			version:  "1.0.0",
			versions: availableVersions,
			want:     []string{"1.0.0"},
			wantErr:  false,
		},
		{
			name:     "range match",
			version:  "^1.0.0",
			versions: availableVersions,
			want:     []string{"1.0.0", "1.1.0", "1.2.0"},
			wantErr:  false,
		},
		{
			name:     "invalid version range",
			version:  "not-a-version",
			versions: availableVersions,
			want:     []string{},
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := GetMatchingVersions(tt.version, tt.versions)
			if (err != nil) != tt.wantErr {
				t.Errorf("GetMatchingVersions() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("GetMatchingVersions() = %v, want %v", got, tt.want)
			}
		})
	}
}
