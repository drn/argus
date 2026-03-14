package project

import (
	"os"
	"path/filepath"
)

// projectSignature maps a file marker to its icon and language.
type projectSignature struct {
	file string
	icon string
	lang string
}

var signatures = []projectSignature{
	{"go.mod", "\ue627", "go"},
	{"Cargo.toml", "\ue7a8", "rust"},
	{"package.json", "\ue718", "node"},
	{"tsconfig.json", "\ue628", "typescript"},
	{"Gemfile", "\ue23e", "ruby"},
	{"requirements.txt", "\ue73c", "python"},
	{"setup.py", "\ue73c", "python"},
	{"pyproject.toml", "\ue73c", "python"},
	{"pom.xml", "\ue256", "java"},
	{"build.gradle", "\ue256", "java"},
	{"mix.exs", "\ue62d", "elixir"},
	{"Makefile", "\ue615", ""},
	{".git", "\ue725", ""},
}

// Detect returns the Nerd Font icon and language name for the project at path.
// Does a single pass over signatures, returning the first match.
func Detect(path string) (icon, lang string) {
	for _, s := range signatures {
		if _, err := os.Stat(filepath.Join(path, s.file)); err == nil {
			return s.icon, s.lang
		}
	}
	return "\uf115", "" // folder icon fallback
}

// DetectIcon returns a Nerd Font icon based on project files found at the given path.
func DetectIcon(path string) string {
	icon, _ := Detect(path)
	return icon
}

// DetectLanguage returns a language name based on project files.
func DetectLanguage(path string) string {
	_, lang := Detect(path)
	return lang
}
