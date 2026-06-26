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
        curl -fL --progress-bar "$url" -o "$dest"
        return
    fi
    if command -v wget >/dev/null 2>&1; then
        wget -O "$dest" "$url"
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

download "$source_url" "$tmp_file"
chmod 0755 "$tmp_file"
mv "$tmp_file" "$target_path"

echo
echo "Installed:"
echo "  $target_path"
echo
"$target_path" --version || true

case ":$PATH:" in
    *":$install_dir:"*) ;;
    *)
        echo
        echo "$install_dir is not on PATH."
        echo "Add this to your shell profile:"
        echo "  export PATH=\"$install_dir:\$PATH\""
        ;;
esac
