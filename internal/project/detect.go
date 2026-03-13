package project

import "os"

// DetectIcon returns a Nerd Font icon based on project files found at the given path.
func DetectIcon(path string) string {
	checks := []struct {
		file string
		icon string
	}{
		{"go.mod", "\ue627"},         // Go
		{"Cargo.toml", "\ue7a8"},     // Rust
		{"package.json", "\ue718"},   // JavaScript/Node
		{"tsconfig.json", "\ue628"},  // TypeScript
		{"Gemfile", "\ue23e"},        // Ruby
		{"requirements.txt", "\ue73c"}, // Python
		{"setup.py", "\ue73c"},       // Python
		{"pyproject.toml", "\ue73c"}, // Python
		{"pom.xml", "\ue256"},        // Java
		{"build.gradle", "\ue256"},   // Java/Gradle
		{"mix.exs", "\ue62d"},        // Elixir
		{"Makefile", "\ue615"},       // Generic/Make
		{".git", "\ue725"},           // Git repo fallback
	}

	for _, c := range checks {
		if _, err := os.Stat(path + "/" + c.file); err == nil {
			return c.icon
		}
	}
	return "\uf115" // folder icon fallback
}

// DetectLanguage returns a language name based on project files.
func DetectLanguage(path string) string {
	checks := []struct {
		file string
		lang string
	}{
		{"go.mod", "go"},
		{"Cargo.toml", "rust"},
		{"package.json", "node"},
		{"tsconfig.json", "typescript"},
		{"Gemfile", "ruby"},
		{"requirements.txt", "python"},
		{"setup.py", "python"},
		{"pyproject.toml", "python"},
		{"pom.xml", "java"},
		{"build.gradle", "java"},
		{"mix.exs", "elixir"},
	}

	for _, c := range checks {
		if _, err := os.Stat(path + "/" + c.file); err == nil {
			return c.lang
		}
	}
	return ""
}
