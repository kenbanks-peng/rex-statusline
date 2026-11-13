package shellinit

import (
	_ "embed"
	"fmt"
	"strings"
)

//go:embed bash.sh
var bash string

//go:embed zsh.sh
var zsh string

func Script(shell, executable string) (string, error) {
	var script string
	switch shell {
	case "bash":
		script = bash
	case "zsh":
		script = zsh
	default:
		return "", fmt.Errorf("unsupported shell %q: use bash or zsh", shell)
	}
	quoted := "'" + strings.ReplaceAll(executable, "'", "'\\''") + "'"
	script = strings.ReplaceAll(script, "@EXECUTABLE@", quoted)
	return "_bsp_init() {\n" + script + "}\n_bsp_init\nunset -f _bsp_init\n", nil
}
