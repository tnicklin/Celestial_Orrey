package simc

import "os"

// writeBytes is a tiny helper used by the parser tests to keep them
// independent of os.WriteFile's import surface.
func writeBytes(path string, data []byte) error {
	return os.WriteFile(path, data, 0o644)
}
