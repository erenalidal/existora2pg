//go:build !windows

package main

// ensureWebView2 is a no-op on non-Windows platforms.
func ensureWebView2() error {
	return nil
}
