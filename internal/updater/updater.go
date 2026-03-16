package updater

import (
	"archive/zip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const (
	// GitHubRepo is the owner/repo for release checks.
	GitHubRepo = "erenalidal/existora2pg"
	// GitHubAPI is the base URL for GitHub API.
	GitHubAPI = "https://api.github.com"
)

// Release represents a GitHub release.
type Release struct {
	TagName     string    `json:"tag_name"`
	Name        string    `json:"name"`
	Body        string    `json:"body"`
	PublishedAt time.Time `json:"published_at"`
	HTMLURL     string    `json:"html_url"`
	Assets      []Asset   `json:"assets"`
}

// Asset represents a release asset (downloadable file).
type Asset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
	Size               int64  `json:"size"`
	ContentType        string `json:"content_type"`
}

// UpdateInfo contains information about an available update.
type UpdateInfo struct {
	Available      bool   `json:"available"`
	CurrentVersion string `json:"current_version"`
	LatestVersion  string `json:"latest_version"`
	ReleaseNotes   string `json:"release_notes,omitempty"`
	ReleaseURL     string `json:"release_url,omitempty"`
	DownloadURL    string `json:"download_url,omitempty"`
	AssetSize      int64  `json:"asset_size,omitempty"`
}

// CheckUpdate checks GitHub for a newer release.
func CheckUpdate(ctx context.Context, currentVersion string) (*UpdateInfo, error) {
	info := &UpdateInfo{CurrentVersion: currentVersion}

	if currentVersion == "" || currentVersion == "dev" {
		info.LatestVersion = "unknown"
		return info, nil
	}

	release, err := fetchLatestRelease(ctx)
	if err != nil {
		return nil, fmt.Errorf("check update: %w", err)
	}

	info.LatestVersion = release.TagName
	info.ReleaseNotes = release.Body
	info.ReleaseURL = release.HTMLURL

	if isNewer(currentVersion, release.TagName) {
		info.Available = true
		asset := findAsset(release.Assets)
		if asset != nil {
			info.DownloadURL = asset.BrowserDownloadURL
			info.AssetSize = asset.Size
		}
	}

	return info, nil
}

// ApplyUpdate downloads and applies the update.
// On Windows: downloads zip, extracts exe, replaces current binary.
// Returns the path to the new binary (caller should restart).
func ApplyUpdate(ctx context.Context, downloadURL string, progressFn func(downloaded, total int64)) error {
	if downloadURL == "" {
		return fmt.Errorf("no download URL provided")
	}

	// Get current executable path
	exePath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("get executable path: %w", err)
	}
	exePath, err = filepath.EvalSymlinks(exePath)
	if err != nil {
		return fmt.Errorf("resolve symlinks: %w", err)
	}

	exeDir := filepath.Dir(exePath)
	exeName := filepath.Base(exePath)

	// Download to temp file
	req, err := http.NewRequestWithContext(ctx, "GET", downloadURL, nil)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("download: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download failed: HTTP %d", resp.StatusCode)
	}

	tmpFile, err := os.CreateTemp(exeDir, "existora-update-*.zip")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tmpPath := tmpFile.Name()
	defer os.Remove(tmpPath)

	// Download with progress
	total := resp.ContentLength
	var downloaded int64
	buf := make([]byte, 32*1024)
	for {
		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			if _, wErr := tmpFile.Write(buf[:n]); wErr != nil {
				tmpFile.Close()
				return fmt.Errorf("write temp: %w", wErr)
			}
			downloaded += int64(n)
			if progressFn != nil {
				progressFn(downloaded, total)
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			tmpFile.Close()
			return fmt.Errorf("download read: %w", readErr)
		}
	}
	tmpFile.Close()

	// Extract the matching exe from zip
	if strings.HasSuffix(downloadURL, ".zip") {
		return extractAndReplace(tmpPath, exeDir, exeName, exePath)
	}

	return fmt.Errorf("unsupported archive format (expected .zip)")
}

// extractAndReplace extracts the zip and replaces the current binary.
func extractAndReplace(zipPath, exeDir, exeName, exePath string) error {
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return fmt.Errorf("open zip: %w", err)
	}
	defer r.Close()

	// Find the exe that matches our binary name in the zip
	var targetFile *zip.File
	for _, f := range r.File {
		name := filepath.Base(f.Name)
		if strings.EqualFold(name, exeName) {
			targetFile = f
			break
		}
	}

	if targetFile == nil {
		// Fallback: look for existora-desktop.exe or existora.exe
		for _, f := range r.File {
			name := filepath.Base(f.Name)
			if strings.Contains(strings.ToLower(name), "existora") && strings.HasSuffix(strings.ToLower(name), ".exe") {
				// Prefer desktop exe
				if strings.Contains(name, "desktop") {
					targetFile = f
					break
				}
				if targetFile == nil {
					targetFile = f
				}
			}
		}
	}

	if targetFile == nil {
		return fmt.Errorf("no matching executable found in zip")
	}

	// Extract to temp location
	rc, err := targetFile.Open()
	if err != nil {
		return fmt.Errorf("open zip entry: %w", err)
	}
	defer rc.Close()

	newExePath := exePath + ".new"
	newFile, err := os.OpenFile(newExePath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0755)
	if err != nil {
		return fmt.Errorf("create new exe: %w", err)
	}

	if _, err := io.Copy(newFile, rc); err != nil {
		newFile.Close()
		os.Remove(newExePath)
		return fmt.Errorf("extract exe: %w", err)
	}
	newFile.Close()

	// Also extract other files (DLLs, CLI exe) to the same directory
	for _, f := range r.File {
		if f.FileInfo().IsDir() {
			continue
		}
		name := filepath.Base(f.Name)
		// Skip the exe we already extracted
		if name == filepath.Base(targetFile.Name) {
			continue
		}
		// Preserve directory structure for lib/oracle/ DLLs
		relPath := f.Name
		// Strip top-level directory if present (e.g., "existora2pg-windows-amd64/lib/oracle/oci.dll")
		parts := strings.SplitN(relPath, "/", 2)
		if len(parts) == 2 {
			relPath = parts[1]
		}
		destPath := filepath.Join(exeDir, relPath)
		destDir := filepath.Dir(destPath)
		if err := os.MkdirAll(destDir, 0755); err != nil {
			continue
		}
		extractFile(f, destPath)
	}

	// Rename: current → .old, new → current
	oldExePath := exePath + ".old"
	os.Remove(oldExePath) // remove previous .old if exists

	// Remove Mark of the Web from extracted exe so SmartScreen won't trigger.
	// Files created by the app itself don't normally get MOTW, but if the
	// source zip was downloaded via browser it may propagate.
	removeMarkOfTheWeb(newExePath)

	if runtime.GOOS == "windows" {
		// On Windows, rename running exe to .old (Windows allows this)
		if err := os.Rename(exePath, oldExePath); err != nil {
			os.Remove(newExePath)
			return fmt.Errorf("rename current exe: %w", err)
		}
		if err := os.Rename(newExePath, exePath); err != nil {
			// Rollback
			os.Rename(oldExePath, exePath)
			return fmt.Errorf("rename new exe: %w", err)
		}
		removeMarkOfTheWeb(exePath)
	} else {
		// On Unix, we can overwrite in-place
		if err := os.Rename(newExePath, exePath); err != nil {
			return fmt.Errorf("replace exe: %w", err)
		}
	}

	return nil
}

// ApplyFromFile applies an update from a local zip file (USB/network share).
// Same as ApplyUpdate but reads from filesystem instead of downloading.
func ApplyFromFile(zipPath string) error {
	exePath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("get executable path: %w", err)
	}
	exePath, err = filepath.EvalSymlinks(exePath)
	if err != nil {
		return fmt.Errorf("resolve symlinks: %w", err)
	}

	exeDir := filepath.Dir(exePath)
	exeName := filepath.Base(exePath)

	return extractAndReplace(zipPath, exeDir, exeName, exePath)
}

// ApplyFromReader applies an update from an io.Reader (e.g. multipart upload).
// Writes to a temp file first, then extracts.
func ApplyFromReader(r io.Reader) error {
	exePath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("get executable path: %w", err)
	}
	exePath, err = filepath.EvalSymlinks(exePath)
	if err != nil {
		return fmt.Errorf("resolve symlinks: %w", err)
	}

	exeDir := filepath.Dir(exePath)
	exeName := filepath.Base(exePath)

	tmpFile, err := os.CreateTemp(exeDir, "existora-update-*.zip")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tmpPath := tmpFile.Name()
	defer os.Remove(tmpPath)

	if _, err := io.Copy(tmpFile, r); err != nil {
		tmpFile.Close()
		return fmt.Errorf("write upload: %w", err)
	}
	tmpFile.Close()

	return extractAndReplace(tmpPath, exeDir, exeName, exePath)
}

func extractFile(f *zip.File, destPath string) error {
	rc, err := f.Open()
	if err != nil {
		return err
	}
	defer rc.Close()

	out, err := os.OpenFile(destPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, f.Mode())
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, rc)
	if err == nil {
		removeMarkOfTheWeb(destPath)
	}
	return err
}

func fetchLatestRelease(ctx context.Context) (*Release, error) {
	url := fmt.Sprintf("%s/repos/%s/releases/latest", GitHubAPI, GitHubRepo)

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github.v3+json")
	req.Header.Set("User-Agent", "existora2pg-updater")

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return &Release{TagName: "v0.0.0"}, nil
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("github API: HTTP %d", resp.StatusCode)
	}

	var release Release
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return nil, fmt.Errorf("decode release: %w", err)
	}
	return &release, nil
}

// findAsset returns the download URL for the current platform's asset.
func findAsset(assets []Asset) *Asset {
	var pattern string
	switch runtime.GOOS {
	case "windows":
		pattern = "windows-amd64.zip"
	case "darwin":
		if runtime.GOARCH == "arm64" {
			pattern = "darwin-arm64.tar.gz"
		} else {
			pattern = "darwin-amd64.tar.gz"
		}
	case "linux":
		pattern = "linux-amd64.tar.gz"
	}

	for i, a := range assets {
		if strings.Contains(a.Name, pattern) {
			return &assets[i]
		}
	}
	return nil
}

// isNewer returns true if latest is newer than current.
// Versions are expected in format "v1.2.3" or "1.2.3".
func isNewer(current, latest string) bool {
	cur := parseVersion(current)
	lat := parseVersion(latest)

	for i := 0; i < 3; i++ {
		if lat[i] > cur[i] {
			return true
		}
		if lat[i] < cur[i] {
			return false
		}
	}
	return false
}

func parseVersion(v string) [3]int {
	v = strings.TrimPrefix(v, "v")
	var parts [3]int
	n, _ := fmt.Sscanf(v, "%d.%d.%d", &parts[0], &parts[1], &parts[2])
	_ = n
	return parts
}
