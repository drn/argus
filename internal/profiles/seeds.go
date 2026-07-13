package profiles

import "embed"

// seedFS embeds the default profile seeds shipped with the argus binary, so
// installing them (InstallDefaults) never depends on a git checkout being
// present on disk — a release-binary install works the same as a
// from-source build.
//
//go:embed seeds/*.toml
var seedFS embed.FS

// SeedNames is the fixed, ordered set of profile seeds embedded in the
// binary.
var SeedNames = []string{"default", "lean", "customer_grade"}
