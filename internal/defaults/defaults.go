// Package defaults embeds the built-in configuration and theme.
package defaults

import _ "embed"

//go:embed config.toml
var Config []byte

//go:embed oh-my-posh.toml
var Theme []byte
