package layout

import (
	"encoding/json"
	"fmt"
)

// Parse parses a layout JSON document and validates the resulting tree.
func Parse(data []byte) (Layout, error) {
	var l Layout
	if err := json.Unmarshal(data, &l); err != nil {
		return Layout{}, fmt.Errorf("parse layout: %w", err)
	}
	if err := Validate(l); err != nil {
		return Layout{}, err
	}
	return l, nil
}

// Validate checks a parsed layout for schema conformance.
func Validate(l Layout) error {
	if l.Name == "" {
		return fmt.Errorf("layout: name is required")
	}
	return validateNode(l.Root, "root")
}

var leafTypes = map[string]struct{}{
	NodeTerminal:    {},
	NodeTaskList:    {},
	NodeGit:         {},
	NodeFile:        {},
	NodeStreampane:  {},
	NodeTaskPreview: {},
	NodeTaskDetail:  {},
}

func validateNode(n Node, path string) error {
	if n.Type == "" {
		return fmt.Errorf("%s: type is required", path)
	}
	if n.Type == NodeSplit {
		return validateSplit(n, path)
	}
	if _, ok := leafTypes[n.Type]; !ok {
		return fmt.Errorf("%s: unknown node type %q", path, n.Type)
	}
	if len(n.Children) != 0 {
		return fmt.Errorf("%s: leaf node %q must not have children", path, n.Type)
	}
	return nil
}

func validateSplit(n Node, path string) error {
	if n.Direction != DirHorizontal && n.Direction != DirVertical {
		return fmt.Errorf("%s: split direction must be %q or %q (got %q)",
			path, DirHorizontal, DirVertical, n.Direction)
	}
	if len(n.Children) < 2 {
		return fmt.Errorf("%s: split must have at least two children", path)
	}
	if len(n.Sizes) != len(n.Children) {
		return fmt.Errorf("%s: split sizes length (%d) does not match children length (%d)",
			path, len(n.Sizes), len(n.Children))
	}
	for i, sz := range n.Sizes {
		if sz <= 0 {
			return fmt.Errorf("%s.sizes[%d]: size must be positive (got %d)", path, i, sz)
		}
	}
	for i, child := range n.Children {
		if err := validateNode(child, fmt.Sprintf("%s.children[%d]", path, i)); err != nil {
			return err
		}
	}
	return nil
}
