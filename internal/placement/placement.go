// Package placement defines the initial floating bar anchor.
package placement

import (
	"fmt"
	"strings"
)

// Anchor is the initial floating bar anchor. The zero value is bottom-left.
type Anchor struct {
	Top        bool
	Horizontal string
	Manual     bool
}

func Parse(s string) (Anchor, error) {
	words := strings.Fields(s)
	if len(words) == 0 {
		return Anchor{}, nil
	}
	if len(words) == 1 && words[0] == "manual" {
		return Anchor{Manual: true}, nil
	}
	p := Anchor{}
	vertical := false
	for _, word := range words {
		switch word {
		case "top", "bottom":
			if vertical {
				return Anchor{}, fmt.Errorf("placement needs one vertical and one horizontal anchor, or manual")
			}
			vertical = true
			p.Top = word == "top"
		case "left", "center", "right":
			if p.Horizontal != "" {
				return Anchor{}, fmt.Errorf("placement needs one vertical and one horizontal anchor, or manual")
			}
			p.Horizontal = word
		default:
			return Anchor{}, fmt.Errorf("invalid placement %q", s)
		}
	}
	if len(words) != 2 || !vertical || p.Horizontal == "" {
		return Anchor{}, fmt.Errorf("placement needs top/bottom and left/center/right, or manual")
	}
	return p, nil
}
