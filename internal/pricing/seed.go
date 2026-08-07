// Package pricing resolves the $/token rate table the cost-estimate feature
// prices accrued usage against. It mirrors internal/profiles's seed/install/
// load shape (embed a default, install-if-absent, in-repo-takes-precedence,
// no caching) applied to a single rates.toml file rather than a named-profile
// directory — see openspec/changes/add-coordinator-cost-estimate/design.md
// Decision 3.
package pricing

import "embed"

// seedFS embeds the default rates.toml shipped with the argus binary, so
// installing it (InstallDefault) never depends on a git checkout being
// present on disk — mirrors internal/profiles/seeds.go's seedFS.
//
//go:embed rates.toml
var seedFS embed.FS

// seedFileName is the embedded seed's path within seedFS, and the filename
// InstallDefault writes at its destination.
const seedFileName = "rates.toml"
