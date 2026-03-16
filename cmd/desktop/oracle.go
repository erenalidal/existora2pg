package main

import (
	"os"
	"path/filepath"
	"runtime"
)

// ensureOracleLibPath checks for Oracle Instant Client libraries next to the binary
// and sets the appropriate library path environment variable if found.
func ensureOracleLibPath() {
	envVar := libraryPathEnvVar()

	if os.Getenv("_EXISTORA_LIB_SET") == "1" {
		return
	}

	if runtime.GOOS != "windows" && os.Getenv(envVar) != "" {
		return
	}

	exe, err := os.Executable()
	if err != nil {
		return
	}
	exe, err = filepath.EvalSymlinks(exe)
	if err != nil {
		return
	}

	binDir := filepath.Dir(exe)
	candidates := []string{
		filepath.Join(binDir, "lib", "oracle"),
		filepath.Join(binDir, "..", "lib", "oracle"),
		binDir,
	}

	// Add common Oracle Instant Client paths
	if runtime.GOOS == "darwin" {
		candidates = append(candidates, "/opt/homebrew/lib", "/usr/local/lib")
	} else if runtime.GOOS == "linux" {
		candidates = append(candidates, "/usr/lib/oracle/21/client64/lib", "/opt/oracle/instantclient")
	}

	libFile := oracleLibName()
	for _, libDir := range candidates {
		if _, err := os.Stat(filepath.Join(libDir, libFile)); err == nil {
			os.Setenv("_EXISTORA_LIB_SET", "1")
			reExecWithLib(envVar, libDir)
			return
		}
	}
}

func reExecWithLib(envVar, libDir string) {
	sep := ":"
	if runtime.GOOS == "windows" {
		sep = ";"
	}

	current := os.Getenv(envVar)
	if current != "" {
		os.Setenv(envVar, libDir+sep+current)
	} else {
		os.Setenv(envVar, libDir)
	}

	exe, _ := os.Executable()
	err := execve(exe, os.Args, os.Environ())
	if err != nil {
		return
	}
}

func libraryPathEnvVar() string {
	switch runtime.GOOS {
	case "darwin":
		return "DYLD_LIBRARY_PATH"
	case "windows":
		return "PATH"
	default:
		return "LD_LIBRARY_PATH"
	}
}

func oracleLibName() string {
	switch runtime.GOOS {
	case "darwin":
		return "libclntsh.dylib"
	case "windows":
		return "oci.dll"
	default:
		return "libclntsh.so"
	}
}
