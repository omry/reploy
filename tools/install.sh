#!/bin/sh
set -eu

repo="omry/reploy"
install_dir="${REPLOY_INSTALL_DIR:-"$HOME/.local/bin"}"
version="${REPLOY_VERSION:-}"

usage() {
    cat <<'EOF'
Usage: install.sh [--to DIR] [--version VERSION]

Downloads the Reploy binary from GitHub Releases and installs it to:
  $HOME/.local/bin/reploy

Options:
  --to DIR           Install into DIR instead of $HOME/.local/bin
  --version VERSION  Install VERSION instead of the repo VERSION
  -h, --help         Show this help
EOF
}

while [ "$#" -gt 0 ]; do
    case "$1" in
        --to)
            if [ "$#" -lt 2 ]; then
                echo "install.sh: --to requires a directory" >&2
                exit 2
            fi
            install_dir="$2"
            shift 2
            ;;
        --version)
            if [ "$#" -lt 2 ]; then
                echo "install.sh: --version requires a version" >&2
                exit 2
            fi
            version="$2"
            shift 2
            ;;
        -h|--help)
            usage
            exit 0
            ;;
        *)
            echo "install.sh: unknown option: $1" >&2
            usage >&2
            exit 2
            ;;
    esac
done

need() {
    if ! command -v "$1" >/dev/null 2>&1; then
        echo "install.sh: missing required command: $1" >&2
        exit 1
    fi
}

detect_target() {
    os="$(uname -s | tr '[:upper:]' '[:lower:]')"
    arch="$(uname -m | tr '[:upper:]' '[:lower:]')"

    case "$os" in
        linux) os="linux" ;;
        darwin) os="darwin" ;;
        *)
            echo "install.sh: unsupported OS: $os" >&2
            exit 1
            ;;
    esac

    case "$arch" in
        x86_64|amd64) arch="amd64" ;;
        arm64|aarch64) arch="arm64" ;;
        *)
            echo "install.sh: unsupported architecture: $arch" >&2
            exit 1
            ;;
    esac

    printf '%s-%s\n' "$os" "$arch"
}

download() {
    url="$1"
    dest="$2"
    if command -v curl >/dev/null 2>&1; then
        curl -fsSL "$url" -o "$dest"
        return
    fi
    if command -v wget >/dev/null 2>&1; then
        wget -nv -O "$dest" "$url"
        return
    fi
    echo "install.sh: missing required command: curl or wget" >&2
    exit 1
}

fetch() {
    url="$1"
    if command -v curl >/dev/null 2>&1; then
        curl -fsSL "$url"
        return
    fi
    if command -v wget >/dev/null 2>&1; then
        wget -qO- "$url"
        return
    fi
    echo "install.sh: missing required command: curl or wget" >&2
    exit 1
}

canonical_path() {
    path="$1"
    dir="${path%/*}"
    base="${path##*/}"
    if [ "$dir" = "$path" ]; then
        dir="."
    fi
    (cd "$dir" 2>/dev/null && printf '%s/%s\n' "$(pwd -P)" "$base") || printf '%s\n' "$path"
}

shell_quote() {
    printf "'"
    printf "%s" "$1" | sed "s/'/'\\\\''/g"
    printf "'"
}

print_path_command() {
    quoted_dir="$(shell_quote "$install_dir")"
    echo "     export PATH=$quoted_dir:\$PATH"
}

print_uninstall_command() {
    quoted_target="$(shell_quote "$target_path")"
    echo "     rm -f $quoted_target"
}

print_uninstall_hint() {
    echo "To uninstall this Reploy command:"
    print_uninstall_command
}

reploy_install_mode() {
    candidate="$1"
    case "$candidate" in
        "$HOME/.local/bin/reploy")
            echo "script install default ($HOME/.local/bin)"
            ;;
        */.venv/bin/reploy|*/venv/bin/reploy)
            echo "Python virtual environment (inferred from path)"
            ;;
        */pipx/venvs/*/bin/reploy|*/.local/pipx/venvs/*/bin/reploy)
            echo "pipx environment (inferred from path)"
            ;;
        *)
            echo ""
            ;;
    esac
}

print_reploy_details() {
    candidate="$1"
    mode="$(reploy_install_mode "$candidate")"
    if [ -n "$mode" ]; then
        echo "Found first installation mode:"
        echo "  $mode"
    fi
    quoted_candidate="$(shell_quote "$candidate")"
    echo "Inspect first command manually:"
    echo "     $quoted_candidate --version"
}

need uname
need mktemp
need chmod
need mkdir
need mv

target="$(detect_target)"
asset="reploy-$target"
if [ -z "$version" ]; then
    version_url="https://raw.githubusercontent.com/$repo/main/VERSION"
    version="$(fetch "$version_url" | tr -d '[:space:]')"
fi
if [ -z "$version" ]; then
    echo "install.sh: could not resolve Reploy version" >&2
    exit 1
fi
case "$version" in
    v*) tag="$version" ;;
    *) tag="v$version" ;;
esac
source_url="https://github.com/$repo/releases/download/$tag/$asset"

target_path="$install_dir/reploy"
tmp_dir="$(mktemp -d)"
tmp_file="$tmp_dir/reploy"
cleanup() {
    rm -rf "$tmp_dir"
}
trap cleanup EXIT INT TERM

cat <<EOF
Installing Reploy
Version: $tag
Platform: $target
Source: $source_url
Target: $target_path

EOF

mkdir -p "$install_dir"
if [ ! -d "$install_dir" ]; then
    echo "install.sh: target directory does not exist: $install_dir" >&2
    exit 1
fi
if [ ! -w "$install_dir" ]; then
    echo "install.sh: target directory is not writable: $install_dir" >&2
    echo "Choose a user-owned directory, for example:" >&2
    echo "  sh install.sh --to \"\$HOME/.local/bin\"" >&2
    exit 1
fi

if ! download "$source_url" "$tmp_file"; then
    echo "install.sh: Reploy release asset was not found or could not be downloaded: $asset in $tag" >&2
    echo "install.sh: this release may not include this target yet: $source_url" >&2
    exit 1
fi
chmod 0755 "$tmp_file"
mv "$tmp_file" "$target_path"

echo
echo "Installed:"
echo "  $target_path"
echo
"$target_path" --version || true
echo
print_uninstall_hint

resolved_reploy="$(command -v reploy 2>/dev/null || true)"
if [ -z "$resolved_reploy" ]; then
    echo
    echo "reploy is not on PATH."
    echo "Options:"
    echo "  1. Correct PATH so this install is found:"
    print_path_command
    echo "  2. Uninstall the command installed by this script:"
    print_uninstall_command
else
    canonical_target="$(canonical_path "$target_path")"
    canonical_resolved="$(canonical_path "$resolved_reploy")"
    if [ "$canonical_resolved" != "$canonical_target" ]; then
        echo
        echo "The installed reploy is not the first reploy on PATH."
        echo "Installed:"
        echo "  $target_path"
        echo "Found first on PATH:"
        echo "  $resolved_reploy"
        print_reploy_details "$resolved_reploy"
        echo "Options:"
        echo "  1. Uninstall the command installed by this script:"
        print_uninstall_command
        echo "  2. Correct PATH so this install is used first:"
        print_path_command
    fi
fi
