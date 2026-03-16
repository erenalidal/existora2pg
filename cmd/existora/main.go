package main

import (
	"os"
	"path/filepath"
	"runtime"

	"github.com/erenalidal/existora2pg/internal/cli"
)

func main() {
	ensureOracleLibPath()
	cli.Execute()
}

// ensureOracleLibPath checks for Oracle Instant Client libraries next to the binary
// and sets the appropriate library path environment variable if found.
// On macOS/Linux, the dynamic linker reads env vars only at process start,
// so if the env var isn't set and the libs exist, we re-exec with the correct env.
// On Windows, DLLs are found from the exe directory or PATH — we prepend to PATH.
func ensureOracleLibPath() {
	envVar := libraryPathEnvVar()

	// If we've already set it (via re-exec marker), don't loop
	if os.Getenv("_EXISTORA_LIB_SET") == "1" {
		return
	}

	// Already set by user — nothing to do
	if runtime.GOOS != "windows" && os.Getenv(envVar) != "" {
		return
	}

	// Find lib/oracle/ relative to the binary
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
		filepath.Join(binDir, "lib", "oracle"),       // bin/lib/oracle/
		filepath.Join(binDir, "..", "lib", "oracle"),  // lib/oracle/ (when bin/ is a subdir)
		binDir, // same directory as binary (Windows typical)
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

// reExecWithLib sets the library path and re-executes the current process.
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
		// If re-exec fails, continue — Oracle calls will fail later with a clear error
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
