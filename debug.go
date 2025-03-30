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
		if len(dep.ResolvedDeps) > 0 {
			prefix := "│   "
			if i == len(deps)-1 {
				prefix = "    "
			}

			// Convert ResolvedDeps map to slice for recursive call
			childDeps := make([]PackageInfo, 0, len(dep.ResolvedDeps))
			for _, pkg := range dep.ResolvedDeps {
				childDeps = append(childDeps, pkg)
			}

			// Recursively render child dependencies
			childTree := RenderDepTree(childDeps)
			for _, line := range strings.Split(childTree, "\n") {
				if line != "" {
					builder.WriteString(prefix + line + "\n")
				}
			}
		}
	}

	return builder.String()
}
