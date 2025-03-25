package main

import (
	"archive/tar"
	"bufio"
	"compress/gzip"
	"context"
	"crypto/sha1"
	"crypto/sha512"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"runtime/pprof"
	"strings"
	"time"

	"golang.org/x/sync/errgroup"
	"golang.org/x/sync/semaphore"
)

type PackageLock struct {
	Name         string                     `json:"name"`
	Version      string                     `json:"version"`
	Dependencies map[string]json.RawMessage `json:"dependencies"`
	Packages     map[string]json.RawMessage `json:"packages"`
}

type PackageInfo struct {
	Version   string            `json:"version"`
	Resolved  string            `json:"resolved,omitempty"`
	Integrity string            `json:"integrity,omitempty"`
	CPU       []string          `json:"cpu,omitempty"`
	OS        []string          `json:"os,omitempty"`
	Optional  bool              `json:"optional,omitempty"`
	Bin       map[string]string `json:"bin,omitempty"`
}

// DepCollection holds all the extracted dependency information
type DepCollection struct {
	DirectDeps      map[string]PackageInfo // Direct dependencies
	AllPackages     map[string]PackageInfo // All packages in the lockfile
	OSSpecificPkgs  map[string][]string    // Map of OS to package names
	CPUSpecificPkgs map[string][]string    // Map of CPU arch to package names
	OptionalPkgs    []string               // List of optional packages
}

func main() {
	if cpuProfilePath := os.Getenv("CPU_PROFILE"); cpuProfilePath != "" {
		f, err := os.Create(cpuProfilePath)
		if err != nil {
			fmt.Printf("Error creating CPU profile file: %v\n", err)
			os.Exit(1)
		}
		pprof.StartCPUProfile(f)
		defer pprof.StopCPUProfile()
		fmt.Printf("CPU profiling enabled, writing to: %s\n", cpuProfilePath)
	}

	usage := `Usage:
  caladan install-lockfile <directory>
  caladan run <directory> <script> <args>`

	if len(os.Args) < 2 {
		fmt.Println(usage)
		os.Exit(1)
	}

	// Check if running on Windows
	if isWindows() {
		fmt.Println("Windows is not supported. Please use Linux or macOS.")
		os.Exit(1)
	}

	if os.Args[1] == "install-lockfile" && len(os.Args) == 3 {
		lockfilePath := filepath.Join(os.Args[2], "package-lock.json")
		InstallLockFile(lockfilePath)
		return
	} else if os.Args[1] == "run" && len(os.Args) >= 4 {
		Run(os.Args[2], os.Args[3:])
		return
	}

	fmt.Println("Invalid command.")
	fmt.Println(usage)
	os.Exit(1)
}

func Run(directory string, args []string) {
	scriptName := args[0]
	scriptArgs := args[1:]

	fmt.Printf("Running %s with args: %v\n", scriptName, scriptArgs)

	// Set up command to run script using project-relative path
	binScriptName := filepath.Join("./node_modules/.bin", scriptName)
	cmd := exec.Command("sh", "-c", binScriptName+" "+strings.Join(scriptArgs, " "))

	// Set working directory to the specified directory (project root)
	cmd.Dir = directory
	fmt.Printf("Working directory: %s\n", directory)

	// Connect standard IO
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin

	// Run the command and wait for it to finish
	err := cmd.Run()

	// Exit with same code as the script
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			os.Exit(exitErr.ExitCode())
		}
		// If not an ExitError, something else went wrong
		fmt.Printf("Error executing script: %v\n", err)
		os.Exit(1)
	}
}

func InstallLockFile(lockfilePath string) {
	data, err := os.ReadFile(lockfilePath)
	if err != nil {
		fmt.Printf("Error reading file: %v\n", err)
		os.Exit(1)
	}

	var packageLock PackageLock
	if err := json.Unmarshal(data, &packageLock); err != nil {
		fmt.Printf("Error parsing JSON: %v\n", err)
		os.Exit(1)
	}

	// Create the collection to hold all dependency info
	deps := DepCollection{
		DirectDeps:      make(map[string]PackageInfo),
		AllPackages:     make(map[string]PackageInfo),
		OSSpecificPkgs:  make(map[string][]string),
		CPUSpecificPkgs: make(map[string][]string),
		OptionalPkgs:    []string{},
	}

	// Process direct dependencies
	if len(packageLock.Dependencies) > 0 {
		for depName, rawData := range packageLock.Dependencies {
			var pkg PackageInfo
			if err := json.Unmarshal(rawData, &pkg); err == nil {
				deps.DirectDeps[depName] = pkg
			}
		}
	}

	// Process all packages
	if len(packageLock.Packages) > 0 {
		for pkgName, rawData := range packageLock.Packages {
			if pkgName == "" {
				// Skip root package
				continue
			}

			var pkg PackageInfo
			if err := json.Unmarshal(rawData, &pkg); err == nil {
				// Add to all packages
				deps.AllPackages[pkgName] = pkg

				// Handle OS specific packages
				for _, os := range pkg.OS {
					deps.OSSpecificPkgs[os] = append(deps.OSSpecificPkgs[os], pkgName)
				}

				// Handle CPU specific packages
				for _, cpu := range pkg.CPU {
					deps.CPUSpecificPkgs[cpu] = append(deps.CPUSpecificPkgs[cpu], pkgName)
				}

				// Handle optional packages
				if pkg.Optional {
					deps.OptionalPkgs = append(deps.OptionalPkgs, pkgName)
				}
			}
		}
	}

	// Get working directory from lockfile path
	workDir := getWorkingDir(lockfilePath)

	// Create/clean node_modules directory
	nodeModulesPath := fmt.Sprintf("%s/node_modules", workDir)
	if err := cleanNodeModules(nodeModulesPath); err != nil {
		fmt.Printf("Error cleaning node_modules: %v\n", err)
		os.Exit(1)
	}

	// Create the node_modules directory
	if err := os.MkdirAll(nodeModulesPath, 0755); err != nil {
		fmt.Printf("Error creating node_modules directory: %v\n", err)
		os.Exit(1)
	}

	// Download and extract packages
	fmt.Println("\nDownloading packages...")
	DownloadPackages(deps.AllPackages, nodeModulesPath)

	fmt.Println("\nInstallation complete!")
}

// isWindows detects if the program is running on Windows
func isWindows() bool {
	return runtime.GOOS == "windows"
}

// getWorkingDir extracts the working directory from the lockfile path
func getWorkingDir(lockfilePath string) string {
	// Get the directory containing the lockfile
	return extractDirPath(lockfilePath)
}

// extractDirPath returns the directory part of a file path
func extractDirPath(filePath string) string {
	// Find the last directory separator
	lastSepIndex := -1
	for i := len(filePath) - 1; i >= 0; i-- {
		if filePath[i] == '/' || filePath[i] == '\\' {
			lastSepIndex = i
			break
		}
	}

	if lastSepIndex == -1 {
		// No directory separator found, return current directory
		return "."
	}

	return filePath[:lastSepIndex]
}

// cleanNodeModules removes the node_modules directory if it exists
func cleanNodeModules(nodeModulesPath string) error {
	// Check if node_modules exists
	if _, err := os.Stat(nodeModulesPath); err == nil {
		fmt.Printf("Removing existing node_modules directory: %s\n", nodeModulesPath)
		return os.RemoveAll(nodeModulesPath)
	} else if !os.IsNotExist(err) {
		return err
	}

	// Directory doesn't exist, nothing to clean
	return nil
}

// DownloadPackages downloads and extracts packages to node_modules
func DownloadPackages(packages map[string]PackageInfo, nodeModulesPath string) {
	// Setup HTTP client with timeout
	client := &http.Client{
		Timeout: 30 * time.Second,
	}

	// Get current OS
	currentOS := runtime.GOOS

	// Create .bin directory
	binDir := filepath.Join(nodeModulesPath, ".bin")
	if err := os.MkdirAll(binDir, 0755); err != nil {
		fmt.Printf("Error creating .bin directory: %v\n", err)
	}

	g, ctx := errgroup.WithContext(context.Background())

	// HTTP concurrency
	httpSemaphore := semaphore.NewWeighted(64)

	// Default to 1.5x cores
	tarWorkers := float64(runtime.NumCPU()) * 1.5
	if tarWorkersEnv := os.Getenv("TAR_WORKERS"); tarWorkersEnv != "" {
		if tw, err := parseFloat64(tarWorkersEnv); err == nil && tw > 0 {
			tarWorkers = tw
			fmt.Printf("Using custom TAR_WORKERS value: %v\n", tarWorkers)
		} else {
			fmt.Printf("Warning: Invalid TAR_WORKERS value '%s', using default: %v\n", tarWorkersEnv, tarWorkers)
		}
	}
	tarSemaphore := semaphore.NewWeighted(int64(tarWorkers))

	// Process each package
	for pkgName, pkgInfo := range packages {
		g.Go(func() error {

			// Skip packages without resolved URLs
			if pkgInfo.Resolved == "" {
				fmt.Printf("Skipping %s: No download URL\n", pkgName)
				return nil
			}

			// Skip OS-specific packages that don't match current OS
			if len(pkgInfo.OS) > 0 {
				// Check if package is OS-specific and not for current OS
				isForCurrentOS := false
				for _, os := range pkgInfo.OS {
					if os == currentOS {
						isForCurrentOS = true
						break
					}
				}

				if !isForCurrentOS {
					fmt.Printf("Skipping %s: Not compatible with %s\n", pkgName, currentOS)
					return nil
				}
			}

			// Extract package name from full path if it includes node_modules prefix
			// e.g., "node_modules/react" -> "react" or "@types/react" -> "@types/react"
			normalizedPkgName := pkgName
			if strings.HasPrefix(normalizedPkgName, "node_modules/") {
				normalizedPkgName = strings.TrimPrefix(normalizedPkgName, "node_modules/")
			}

			// Create package directory
			// For scoped packages like @babel/core, we need to handle the @ symbol
			pkgPath := filepath.Join(nodeModulesPath, normalizedPkgName)
			if err := os.MkdirAll(pkgPath, 0755); err != nil {
				return fmt.Errorf("error creating directory for %s: %v\n", normalizedPkgName, err)
			}

			// Download the package tarball
			err := downloadAndExtractPackage(ctx, httpSemaphore, tarSemaphore, client, pkgInfo.Resolved, pkgInfo.Integrity, pkgPath)
			if err != nil {
				return fmt.Errorf("error downloading/extracting %s: %v\n", normalizedPkgName, err)
			}

			return nil
		})
	}

	// Wait for all packages to complete
	if err := g.Wait(); err != nil {
		fmt.Printf("Error during package downloads: %v\n", err)
		os.Exit(1)
	}

	// Setup bin scripts after all packages are downloaded
	setupBinScripts(packages, nodeModulesPath)
}

// downloadAndExtractPackage downloads a package tarball and extracts it
func downloadAndExtractPackage(ctx context.Context, httpSemaphore, tarSemaphore *semaphore.Weighted, client *http.Client, url, integrity, destPath string) error {
	httpSemaphore.Acquire(ctx, 1)
	defer httpSemaphore.Release(1)

	// Download and extract the package
	fmt.Printf("Downloading %s\n", url)

	// Download the tarball
	resp, err := client.Get(url)
	if err != nil {
		return fmt.Errorf("error downloading package: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download failed with status: %s", resp.Status)
	}

	// Setup hash verification
	var hash interface {
		io.Writer
		Sum() []byte
	}

	if strings.HasPrefix(integrity, "sha1-") {
		h := sha1.New()
		hash = &shaWrapper{h, func() []byte { return h.Sum(nil) }}
	} else if strings.HasPrefix(integrity, "sha512-") {
		h := sha512.New()
		hash = &shaWrapper{h, func() []byte { return h.Sum(nil) }}
	} else {
		return fmt.Errorf("unsupported integrity check: %s", integrity)
	}

	// Use a TeeReader to compute hash while reading
	teeReader := io.TeeReader(resp.Body, hash)
	reader := teeReader

	// Extract directly from the download stream
	tarSemaphore.Acquire(ctx, 1)
	defer tarSemaphore.Release(1)
	fmt.Printf("Extracting %s\n", destPath)
	err = extractTarGz(reader, destPath)
	if err != nil {
		return fmt.Errorf("error extracting package: %v", err)
	}

	// Calculate expected hash from integrity string
	expectedHashBase64 := strings.Split(integrity, "-")[1]
	expectedHash, err := base64.StdEncoding.DecodeString(expectedHashBase64)
	if err != nil {
		return fmt.Errorf("error decoding integrity hash: %v", err)
	}

	// Compare with actual hash
	actualHash := hash.Sum()
	if !compareHashes(actualHash, expectedHash) {
		return fmt.Errorf("integrity check failed")
	}

	return nil
}

// shaWrapper is a helper to make different hash implementations behave the same
type shaWrapper struct {
	w io.Writer
	f func() []byte
}

func (s *shaWrapper) Write(p []byte) (n int, err error) {
	return s.w.Write(p)
}

func (s *shaWrapper) Sum() []byte {
	return s.f()
}

// compareHashes compares two byte slices for equality
func compareHashes(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// extractTarGz extracts a tar.gz file to the destination path
func extractTarGz(src io.Reader, destPath string) error {
	// Use buffered I/O for better performance
	bufReader := bufio.NewReaderSize(src, 1<<20) // 1MB buffer

	// Create a gzip reader
	gzr, err := gzip.NewReader(bufReader)
	if err != nil {
		return fmt.Errorf("error creating gzip reader: %v", err)
	}
	defer gzr.Close()

	// Create a tar reader with a buffer
	tr := tar.NewReader(gzr)

	// Create a map to track directories we've already created
	// to avoid redundant MkdirAll calls
	createdDirs := make(map[string]bool)

	// Predefine value to reduce allocations in loop
	packagePrefix := "package/"

	// Process each file in tarball
	for {
		header, err := tr.Next()
		if err == io.EOF {
			break // End of archive
		}
		if err != nil {
			return fmt.Errorf("error reading tar: %v", err)
		}

		// Skip package dir prefix (usually "package/")
		// npm packages have "package" folder at tarball root
		name := header.Name
		if strings.HasPrefix(name, packagePrefix) {
			name = name[len(packagePrefix):] // Faster than TrimPrefix
		}

		// Skip empty names
		if name == "" {
			continue
		}

		// Build target path
		target := filepath.Join(destPath, name)

		switch header.Typeflag {
		case tar.TypeDir:
			// Create dirs with proper perms
			if !createdDirs[target] {
				if err := os.MkdirAll(target, 0755); err != nil {
					return fmt.Errorf("error creating directory %s: %v", target, err)
				}
				createdDirs[target] = true
			}

		case tar.TypeReg, tar.TypeRegA:
			// Create dir for file if needed
			dir := filepath.Dir(target)
			if !createdDirs[dir] {
				if err := os.MkdirAll(dir, 0755); err != nil {
					return fmt.Errorf("error creating directory for file %s: %v", target, err)
				}
				createdDirs[dir] = true
			}

			// Create file with buffer for better perf
			f, err := os.OpenFile(target, os.O_CREATE|os.O_RDWR, os.FileMode(header.Mode))
			if err != nil {
				return fmt.Errorf("error creating file %s: %v", target, err)
			}

			// Use buffered I/O for file writing
			bufWriter := bufio.NewWriterSize(f, 1<<16) // 64KB buffer

			// Copy content
			_, err = io.Copy(bufWriter, tr)
			if err != nil {
				bufWriter.Flush()
				f.Close()
				return fmt.Errorf("error writing to file %s: %v", target, err)
			}

			// Ensure all data written
			if err = bufWriter.Flush(); err != nil {
				f.Close()
				return fmt.Errorf("error flushing buffer for file %s: %v", target, err)
			}

			if err := f.Close(); err != nil {
				return fmt.Errorf("error closing file %s: %v", target, err)
			}

		case tar.TypeSymlink:
			// Create dir for symlink if needed
			dir := filepath.Dir(target)
			if !createdDirs[dir] {
				if err := os.MkdirAll(dir, 0755); err != nil {
					return fmt.Errorf("error creating directory for symlink %s: %v", target, err)
				}
				createdDirs[dir] = true
			}

			// Remove existing symlink to avoid errors
			err = os.Remove(target)
			if err != nil {
				return fmt.Errorf("error removing existing symlink %s: %v", target, err)
			}

			if err := os.Symlink(header.Linkname, target); err != nil {
				// If symlink creation fails, create text file with link info
				linkInfo := fmt.Sprintf("Symlink to: %s", header.Linkname)
				if writeErr := os.WriteFile(target+".symlink", []byte(linkInfo), 0644); writeErr != nil {
					return fmt.Errorf("error creating symlink placeholder for %s: %v", target, writeErr)
				}
			}
		}
	}

	return nil
}

// setupBinScripts creates symlinks for executable scripts in node_modules/.bin
func setupBinScripts(packages map[string]PackageInfo, nodeModulesPath string) {
	binDir := filepath.Join(nodeModulesPath, ".bin")
	fmt.Println("\nSetting up bin scripts...")

	for pkgName, pkgInfo := range packages {
		if len(pkgInfo.Bin) == 0 {
			// Check for package.json to extract bin info
			packageJSONPath := filepath.Join(nodeModulesPath, pkgName, "package.json")
			binInfo, err := readPackageJSONBin(packageJSONPath, pkgName)
			if err != nil || len(binInfo) == 0 {
				continue // No bin scripts for this package
			}
			pkgInfo.Bin = binInfo
		}

		// Process bin entries
		for cmdName, scriptPath := range pkgInfo.Bin {
			// Skip if empty
			if cmdName == "" || scriptPath == "" {
				continue
			}

			// Normalize package name from full path if necessary
			normalizedPkgName := pkgName
			if strings.HasPrefix(normalizedPkgName, "node_modules/") {
				normalizedPkgName = strings.TrimPrefix(normalizedPkgName, "node_modules/")
			}

			// Get absolute path to the script
			scriptFullPath := filepath.Join(nodeModulesPath, normalizedPkgName, scriptPath)
			binLinkPath := filepath.Join(binDir, cmdName)

			// Verify script file exists and is readable
			if _, err := os.Stat(scriptFullPath); err != nil {
				fmt.Printf("Warning: Script %s not found for %s: %v\n", scriptPath, cmdName, err)
				continue
			}

			// Create the symlink
			if err := createExecutableSymlink(scriptFullPath, binLinkPath); err != nil {
				fmt.Printf("Error creating symlink for %s: %v\n", cmdName, err)
			} else {
				// Verify the symlink was created successfully
				if _, err := os.Lstat(binLinkPath); err != nil {
					fmt.Printf("Warning: Symlink verification failed for %s: %v\n", cmdName, err)
				} else {
					fmt.Printf("Created bin script: %s -> %s\n", cmdName, scriptFullPath)
				}
			}
		}
	}
}

// readPackageJSONBin reads the bin field from a package.json file
func readPackageJSONBin(packageJSONPath, pkgName string) (map[string]string, error) {
	data, err := os.ReadFile(packageJSONPath)
	if err != nil {
		return nil, err
	}

	var packageJSON struct {
		Name string      `json:"name"`
		Bin  interface{} `json:"bin"`
	}

	if err := json.Unmarshal(data, &packageJSON); err != nil {
		return nil, err
	}

	binMap := make(map[string]string)

	// Handle bin field which can be either a string or a map
	switch v := packageJSON.Bin.(type) {
	case string:
		// If bin is a string, use the package name as the command name
		name := packageJSON.Name
		if name == "" {
			// Extract name from package path if not specified
			parts := strings.Split(pkgName, "/")
			name = parts[len(parts)-1]
		}
		binMap[name] = v

	case map[string]interface{}:
		// If bin is a map, convert it to our format
		for cmd, script := range v {
			if scriptPath, ok := script.(string); ok {
				binMap[cmd] = scriptPath
			}
		}
	}

	return binMap, nil
}

// createExecutableSymlink creates a symlink and ensures the target is executable
func createExecutableSymlink(targetPath, linkPath string) error {
	// Check if the target exists
	if _, err := os.Stat(targetPath); err != nil {
		return fmt.Errorf("target script not found: %v", err)
	}

	// Make the target executable
	if err := os.Chmod(targetPath, 0755); err != nil {
		return fmt.Errorf("failed to make script executable: %v", err)
	}

	// Remove existing symlink if it exists
	if _, err := os.Lstat(linkPath); err == nil {
		if err := os.Remove(linkPath); err != nil {
			return fmt.Errorf("failed to remove existing symlink: %v", err)
		}
	}

	// Create a relative path from linkPath to targetPath
	// This ensures the symlink works when the directory is copied elsewhere
	linkDir := filepath.Dir(linkPath)
	relTargetPath, err := filepath.Rel(linkDir, targetPath)
	if err != nil {
		return fmt.Errorf("failed to create relative path: %v", err)
	}

	// Create the symlink with the relative path
	if err := os.Symlink(relTargetPath, linkPath); err != nil {
		return fmt.Errorf("failed to create symlink: %v", err)
	}

	return nil
}

// parseFloat64 safely parses a string to float64
func parseFloat64(s string) (float64, error) {
	var result float64
	_, err := fmt.Sscanf(s, "%f", &result)
	return result, err
}
