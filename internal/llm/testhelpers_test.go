package llm

import "os"

func writeFile(path, body string) error {
	return os.WriteFile(path, []byte(body), 0o600)
}

func chmod(path string, mode os.FileMode) error {
	return os.Chmod(path, mode)
}
