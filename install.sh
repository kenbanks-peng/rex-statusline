#!/bin/sh
set -eu

fail() {
  printf '%s\n' "$*" >&2
  exit 1
}

os=$(uname -s)
case "$os" in
  Darwin) os=darwin ;;
  Linux) os=linux ;;
  *) fail "Unsupported OS: $os" ;;
esac
arch=$(uname -m)
case "$arch" in
  x86_64) arch=amd64 ;;
  arm64|aarch64) arch=arm64 ;;
  *) fail "Unsupported architecture: $arch" ;;
esac

for command in curl tar install mktemp awk; do
  command -v "$command" >/dev/null 2>&1 || fail "Required command not found: $command"
done
if command -v sha256sum >/dev/null 2>&1; then
  checksum=sha256sum
elif command -v shasum >/dev/null 2>&1; then
  checksum=shasum
else
  fail "Required command not found: sha256sum or shasum"
fi

archive="rex-statusline-$os-$arch.tar.gz"
url=https://github.com/kenbanks-peng/rex-statusline/releases/latest/download
destination="$HOME/.local/bin"
tmp=$(mktemp -d)
staged=
trap 'rm -rf "$tmp"; if [ -n "$staged" ]; then rm -f "$staged"; fi' 0
trap 'exit 1' HUP INT TERM

(
  cd "$tmp"
  curl -fsSL -o "$archive" "$url/$archive"
  curl -fsSL -o checksums.txt "$url/checksums.txt"
  awk -v archive="$archive" '
    $2 == archive { print; count++ }
    END { if (count != 1) exit 1 }
  ' checksums.txt > checksum.txt || fail "Missing or duplicate checksum for $archive"
  if [ "$checksum" = sha256sum ]; then
    sha256sum -c checksum.txt
  else
    shasum -a 256 -c checksum.txt
  fi
  tar -xzf "$archive" rex-statusline
)

mkdir -p "$destination"
staged=$(mktemp "$destination/.rex-statusline.XXXXXX")
install -m 755 "$tmp/rex-statusline" "$staged"
mv -f "$staged" "$destination/rex-statusline"
printf 'Installed %s/rex-statusline\n' "$destination"
case ":${PATH:-}:" in
  *:"$destination":*) ;;
  *) printf 'Add %s to your PATH.\n' "$destination" ;;
esac
