# rex-statusline

A status line for [Rex](https://www.superlogical.com/rex/docs), rendered with [Oh My Posh](https://github.com/JanDeDobbeleer/oh-my-posh), shared by terminal splits.

![Rex statusline screenshot](docs/images/rex-statusline.png)


## Pre-requisites

Requires [Rex](https://www.superlogical.com/rex/docs) and [Oh My Posh](https://ohmyposh.dev/docs/installation).


## Installation

```sh
curl -fsSL https://raw.githubusercontent.com/kenbanks-peng/rex-statusline/main/install.sh | sh
```


### Configuration

Ensure that `rex`, `oh-my-posh`, and `~/.local/bin` are on your `PATH`.

Add the appropriate shell integration:
- for zsh, add `eval "$(rex-statusline init zsh)"` to your .zshrc file.
- for bash, add `eval "$(rex-statusline init bash)"` to your .bashrc


Use `rex-statusline` without config files or create config files using: `rex-statusline config init`


## Use

On macOS or Linux with systemd, start the status lines and enable startup at login:

```sh
rex-statusline add
```

Stop the status lines:

```sh
rex-statusline remove
```

Stopping leaves login startup enabled. Linux uses a systemd user service; no sudo is needed.

Drag any bar to move all bars.

Check the installed version with `rex-statusline version` or `rex-statusline --version`.
Release builds report their Git tag; local builds report `dev`.


## Development

Requires Go 1.24+.

Run the manager directly for debugging (also works without systemd):

```sh
rex-statusline manage --foreground
```

Stop with Ctrl-C. Stop the background service first with `rex-statusline remove` if it is running.

```sh
go build -o build/rex-statusline ./cmd/rex-statusline
go test ./...
```

With [mise](https://mise.jdx.dev/), `mise run test` runs the full test suite. `mise run cross` builds for macOS and Linux on amd64 and arm64.

See [release automation](docs/releases.md) for publishing and Homebrew token setup.

See [BSP](docs/BSP.md) for remote and container shell context, and [Rex integration](docs/rex-integration.md) for integration details.
