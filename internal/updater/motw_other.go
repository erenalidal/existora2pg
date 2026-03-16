//go:build !windows

package updater

// removeMarkOfTheWeb is a no-op on non-Windows platforms.
func removeMarkOfTheWeb(_ string) {}
