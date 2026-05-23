// Package layout owns argus's TUI layout descriptors and the registry that
// holds them. Layouts are pure data — a tree of [Node] values describing
// splits and leaf panels — that downstream renderers (built-in or
// plugin-introduced) turn into tview primitives.
//
// PR 6 ships the data types, JSON parser, and registry plus filesystem
// loader. The default layout (the existing three-panel task page) is
// registered as [DefaultLayoutName]; user layouts loaded from
// ~/.argus/layouts/*.json are parsed and held but are not yet rendered.
// PR 7 wires them through Settings so the user can pick a layout.
package layout

// DefaultLayoutName is the registry key for the built-in three-panel task
// page. Boot always renders this layout; user-supplied layouts are inert
// in PR 6.
const DefaultLayoutName = "tasks-default"

// Node types accepted by the layout JSON schema v1.
const (
	NodeSplit      = "split"
	NodeTerminal   = "terminal"
	NodeTaskList   = "task-list"
	NodeGit        = "git"
	NodeFile       = "file"
	NodeStreampane = "streampane"
	// NodeTaskPreview and NodeTaskDetail are the two argus-specific leaf
	// panels that make up the default layout. They are intentionally not in
	// the plugin contract reference table — plugins describe panels via the
	// generic types above. They appear here so the default layout can be
	// expressed as data alongside user layouts.
	NodeTaskPreview = "task-preview"
	NodeTaskDetail  = "task-detail"
)

// Split direction constants for [Node.Direction].
const (
	DirHorizontal = "horizontal"
	DirVertical   = "vertical"
)

// Layout is a named layout descriptor.
type Layout struct {
	Name    string            `json:"name"`
	Title   string            `json:"title"`
	Root    Node              `json:"root"`
	Hotkeys map[string]string `json:"hotkeys,omitempty"`
}

// Node is one node in a layout tree. A node is either an internal split
// (with [NodeSplit] type and Children populated) or a leaf panel (one of
// the typed leaf constants above, with Children empty).
type Node struct {
	Type      string `json:"type"`
	Direction string `json:"direction,omitempty"`
	Sizes     []int  `json:"sizes,omitempty"`
	Children  []Node `json:"children,omitempty"`
	// Bind is used by terminal leaves: "task:<id>" or "meta:<key>=<value>".
	Bind string `json:"bind,omitempty"`
	// Cycle is used by terminal leaves to indicate the panel cycles
	// through matching tasks rather than pinning to one.
	Cycle bool `json:"cycle,omitempty"`
	// Source is used by streampane leaves: "callback:<url>" or "file:<path>".
	Source string `json:"source,omitempty"`
}

// IsSplit reports whether this node is an internal split (has children).
func (n Node) IsSplit() bool { return n.Type == NodeSplit }
