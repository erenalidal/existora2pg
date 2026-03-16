//go:build windows

package main

import (
	"embed"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"golang.org/x/sys/windows/registry"
)

//go:embed resources/MicrosoftEdgeWebview2Setup.exe
var webview2Bootstrapper embed.FS

// ensureWebView2 checks if WebView2 Runtime is installed.
// If not, tries to install using:
// 1. Offline installer next to the exe (MicrosoftEdgeWebView2RuntimeInstallerX64.exe)
// 2. Embedded bootstrapper (needs internet)
func ensureWebView2() error {
	if isWebView2Installed() {
		return nil
	}

	fmt.Println("WebView2 Runtime not found. Installing...")

	// Try offline installer next to exe first
	exe, _ := os.Executable()
	exeDir := filepath.Dir(exe)
	offlineInstaller := filepath.Join(exeDir, "MicrosoftEdgeWebView2RuntimeInstallerX64.exe")

	if _, err := os.Stat(offlineInstaller); err == nil {
		fmt.Println("Found offline installer, running...")
		cmd := exec.Command(offlineInstaller, "/silent", "/install")
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err == nil {
			fmt.Println("WebView2 Runtime installed successfully.")
			return nil
		}
		fmt.Println("Offline installer failed, trying embedded bootstrapper...")
	}

	// Fallback: embedded bootstrapper (needs internet)
	data, err := webview2Bootstrapper.ReadFile("resources/MicrosoftEdgeWebview2Setup.exe")
	if err != nil {
		return fmt.Errorf("failed to read embedded bootstrapper: %w", err)
	}

	tmpDir := os.TempDir()
	bootstrapperPath := filepath.Join(tmpDir, "MicrosoftEdgeWebview2Setup.exe")
	if err := os.WriteFile(bootstrapperPath, data, 0755); err != nil {
		return fmt.Errorf("failed to write bootstrapper: %w", err)
	}
	defer os.Remove(bootstrapperPath)

	cmd := exec.Command(bootstrapperPath, "/silent", "/install")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("WebView2 installation failed: %w", err)
	}

	fmt.Println("WebView2 Runtime installed successfully.")
	return nil
}

func isWebView2Installed() bool {
	paths := []struct {
		root registry.Key
		path string
	}{
		{registry.LOCAL_MACHINE, `SOFTWARE\WOW6432Node\Microsoft\EdgeUpdate\Clients\{F3017226-FE2A-4295-8BEB-235B8DE50060}`},
		{registry.LOCAL_MACHINE, `SOFTWARE\Microsoft\EdgeUpdate\Clients\{F3017226-FE2A-4295-8BEB-235B8DE50060}`},
		{registry.CURRENT_USER, `SOFTWARE\Microsoft\EdgeUpdate\Clients\{F3017226-FE2A-4295-8BEB-235B8DE50060}`},
	}

	for _, p := range paths {
		key, err := registry.OpenKey(p.root, p.path, registry.QUERY_VALUE)
		if err == nil {
			val, _, err := key.GetStringValue("pv")
			key.Close()
			if err == nil && val != "" && val != "0.0.0.0" {
				return true
			}
		}
	}
	return false
}
