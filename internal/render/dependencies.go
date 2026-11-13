package render

import (
	"encoding/json"
	"strings"
	"text/template"
	"text/template/parse"
)

type nativeDependencies uint8

const (
	nativeHost nativeDependencies = 1 << iota
	nativePython
	nativeJavaScript
	nativeGit
	allNative = nativeHost | nativePython | nativeJavaScript | nativeGit
)

// Native probe selection and OMP cache eligibility are independent. Audited
// filesystem-dependent built-ins need only their explicit native Env references,
// but still run OMP on every poll. Unknown features retain all probes and disable
// caching because their native environment reads cannot be proved complete.
func themeDependencies(theme map[string]any) (nativeDependencies, bool) {
	data, err := json.Marshal(theme)
	if err != nil {
		return allNative, false
	}
	var decoded map[string]any
	if json.Unmarshal(data, &decoded) != nil {
		return allNative, false
	}
	var deps nativeDependencies
	cacheable := true
	var check func(any, string, string) bool
	check = func(value any, kind, segment string) bool {
		switch v := value.(type) {
		case map[string]any:
			if kind == "segment" {
				segment, _ = v["type"].(string)
				switch segment {
				case "text", "status", "executiontime":
				case "os", "go", "lua", "zig", "docker":
					// OMP 32: OS/platform discovery, language file activation and
					// version commands, Docker activation/config are external
					// dependencies. None implicitly consumes Rex's native values.
					cacheable = false
				default:
					return false
				}
			}
			for key, child := range v {
				next := kind
				switch kind {
				case "theme":
					switch key {
					case "$schema", "version", "final_space", "shell_integration", "palette":
						if key == "palette" {
							next = "palette"
						}
					case "blocks":
						next = "block"
					default:
						return false
					}
				case "block":
					switch key {
					case "type", "alignment", "newline":
					case "segments":
						next = "segment"
					default:
						return false
					}
				case "segment":
					switch key {
					case "type":
					case "style", "template", "foreground", "background", "foreground_templates", "background_templates",
						"leading_diamond", "trailing_diamond", "powerline_symbol", "leading_powerline_symbol",
						"trailing_powerline_symbol", "invert_powerline":
					case "options":
						next = "options"
					default:
						return false
					}
				case "options":
					switch key {
					case "always_enabled", "threshold", "style", "precision":
					case "display_mode":
						if segment != "docker" || child != "files" {
							return false
						}
					default:
						return false
					}
				}
				if !check(child, next, segment) {
					return false
				}
			}
		case []any:
			for _, child := range v {
				if !check(child, kind, segment) {
					return false
				}
			}
		case string:
			if strings.Contains(v, "{{") {
				found, ok := templateDependencies(v, segment)
				deps |= found
				return ok
			}
		}
		return true
	}
	if !check(decoded, "theme", "") {
		return allNative, false
	}
	return deps, cacheable
}

func templateDependencies(source, segment string) (nativeDependencies, bool) {
	t, err := template.New("dependency").Parse(source)
	if err != nil {
		return allNative, false
	}
	var deps nativeDependencies
	var visit func(parse.Node) bool
	visit = func(node parse.Node) bool {
		if node == nil {
			return true
		}
		switch n := node.(type) {
		case *parse.ListNode:
			if n == nil {
				return true
			}
			for _, child := range n.Nodes {
				if !visit(child) {
					return false
				}
			}
		case *parse.ActionNode:
			return visit(n.Pipe)
		case *parse.PipeNode:
			if len(n.Decl) != 0 {
				return false
			}
			for _, command := range n.Cmds {
				if !visit(command) {
					return false
				}
			}
		case *parse.CommandNode:
			for _, arg := range n.Args {
				if !visit(arg) {
					return false
				}
			}
		case *parse.IfNode:
			return visit(n.Pipe) && visit(n.List) && visit(n.ElseList)
		case *parse.FieldNode:
			if len(n.Ident) == 2 && n.Ident[0] == "Env" {
				switch n.Ident[1] {
				case "POSH_HOST_LABEL":
					deps |= nativeHost
				case "PYTHON_SEGMENT":
					deps |= nativePython
				case "JS_SEGMENT":
					deps |= nativeJavaScript
				case "GITSTATUS", "GITSTATUS_CHANGED", "GITSTATUS_AHEAD", "GITSTATUS_BEHIND":
					deps |= nativeGit
				}
				return true
			}
			if len(n.Ident) != 1 {
				return false
			}
			switch n.Ident[0] {
			case "Code", "Error", "Meaning", "FormattedMs":
			default:
				if !((segment == "os" && n.Ident[0] == "Icon") ||
					((segment == "go" || segment == "lua" || segment == "zig") && n.Ident[0] == "Full") ||
					(segment == "docker" && n.Ident[0] == "Context")) {
					return false
				}
			}
		case *parse.IdentifierNode:
			switch n.Ident {
			case "eq", "ne", "gt", "ge", "lt", "le", "and", "or", "not", "printf", "print", "println", "len":
			default:
				return false
			}
		case *parse.TextNode, *parse.StringNode, *parse.NumberNode, *parse.BoolNode, *parse.NilNode:
		default:
			return false
		}
		return true
	}
	// Definitions could hide work outside the root tree.
	if len(t.Templates()) != 1 || !visit(t.Tree.Root) {
		return allNative, false
	}
	return deps, true
}
