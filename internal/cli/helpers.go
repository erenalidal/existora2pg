package cli

import "strings"

// maskDSN masks the password in a DSN string for safe logging.
func maskDSN(dsn string) string {
	// Handle oracle://user:pass@host format
	if idx := strings.Index(dsn, "://"); idx >= 0 {
		rest := dsn[idx+3:]
		if atIdx := strings.Index(rest, "@"); atIdx >= 0 {
			userPass := rest[:atIdx]
			if colonIdx := strings.Index(userPass, ":"); colonIdx >= 0 {
				return dsn[:idx+3] + userPass[:colonIdx+1] + "***@" + rest[atIdx+1:]
			}
		}
	}
	return dsn
}
