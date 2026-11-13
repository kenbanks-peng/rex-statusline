# Launcher helpers: source on the HOST, never inside the guest.
# Caller retains one token per launch and supplies its stable local owner PID.
# Guest transport is separate from the terminal's ordinary stdout.
bsp_bridge_claim() {
  emulate -L zsh
  local owner=$1 source=$2
  [[ $source == (remote|container) ]] || return 1
  command rex-statusline claim --owner "$owner" --source "$source"
}
# stdin: the fresh rendered line only; identity and ownership stay on the host.
# Preserve token/owner from claim; never derive them from guest data.
bsp_bridge_publish() {
  emulate -L zsh
  local token=$1 owner=$2 source=$3
  command rex-statusline publish --kind line --epoch "$token" --owner "$owner" --source "$source"
}
bsp_bridge_release() {
  emulate -L zsh
  local token=$1 owner=$2 source=$3
  command rex-statusline release --epoch "$token" \
    --owner "$owner" --source "$source"
}
