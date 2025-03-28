package main

import (
	"fmt"
	"strings"
)

func RenderDepTree(deps []PackageInfo) string {
	var builder strings.Builder

	for i, dep := range deps {
		// Add connector based on position
		if i == len(deps)-1 {
			builder.WriteString("└── ")
		} else {
			builder.WriteString("├── ")
		}

		// Add package name and version
		builder.WriteString(fmt.Sprintf("%s@%s\n", dep.Name, dep.Version))

		// Recursively render dependencies with proper indentation
		if len(dep.Dependencies) > 0 {
			prefix := "│   "
			if i == len(deps)-1 {
				prefix = "    "
			}

			// Convert dep.Dependencies map to []PackageInfo for recursive call
			childDeps := make([]PackageInfo, 0, len(dep.Dependencies))
			for name, version := range dep.Dependencies {
				childDeps = append(childDeps, PackageInfo{
					Name:    name,
					Version: version,
				})
			}

			// Recursively render child dependencies
			childTree := RenderDepTree(childDeps)
			// Add proper indentation to each line
			for _, line := range strings.Split(childTree, "\n") {
				if line != "" {
					builder.WriteString(prefix + line + "\n")
				}
			}
		}
	}

	return builder.String()
}
