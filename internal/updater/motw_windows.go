package updater

import "os"

// removeMarkOfTheWeb removes the Zone.Identifier alternate data stream (MOTW)
// from a file on Windows. This prevents SmartScreen from showing "Unknown publisher"
// warnings on files extracted by the application itself.
func removeMarkOfTheWeb(path string) {
	// Zone.Identifier is stored as an NTFS alternate data stream
	os.Remove(path + ":Zone.Identifier")
}
