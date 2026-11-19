#!/bin/sh
# Installs pais-ssh from its GitHub Releases into ~/.local/bin, on Linux or macOS. Run again to upgrade.
#
#   curl -fsSL https://raw.githubusercontent.com/drai-inn/pais-ssh/main/install.sh | sh
#   curl -fsSL https://raw.githubusercontent.com/drai-inn/pais-ssh/main/install.sh | sh -s -- v0.2.2
#
# A file fetched with curl is not quarantined, so macOS runs the unsigned binary without a Gatekeeper prompt.
set -eu

# Everything runs from main, so a download cut short never runs half the script.
main() {
	repo=https://github.com/drai-inn/pais-ssh
	bin_dir="$HOME/.local/bin"

	case "$(uname -s)" in
	Linux) os=linux ;;
	Darwin) os=darwin ;;
	*) fail "unsupported system $(uname -s): download pais-ssh from $repo/releases" ;;
	esac
	case "$(uname -m)" in
	x86_64 | amd64) arch=amd64 ;;
	arm64 | aarch64) arch=arm64 ;;
	*) fail "unsupported processor $(uname -m)" ;;
	esac

	if [ $# -gt 0 ]; then
		url="$repo/releases/download/$1"
	else
		url="$repo/releases/latest/download"
	fi
	archive="pais-ssh_${os}_${arch}.tar.gz"

	tmp=$(mktemp -d)
	trap 'rm -rf "$tmp"' EXIT
	echo "Downloading $url/$archive"
	curl -fsSL -o "$tmp/$archive" "$url/$archive" || fail "download failed"
	curl -fsSL -o "$tmp/checksums.txt" "$url/checksums.txt" || fail "download of checksums.txt failed"

	want=$(awk -v f="$archive" '$2 == f { print $1 }' "$tmp/checksums.txt")
	if command -v sha256sum >/dev/null; then
		got=$(sha256sum "$tmp/$archive" | cut -d' ' -f1)
	else
		got=$(shasum -a 256 "$tmp/$archive" | cut -d' ' -f1)
	fi
	[ -n "$want" ] && [ "$want" = "$got" ] || fail "checksum mismatch for $archive"

	tar -xzf "$tmp/$archive" -C "$tmp" pais-ssh
	mkdir -p "$bin_dir"
	# Replace by rename, so an ssh running the old binary keeps it.
	mv "$tmp/pais-ssh" "$bin_dir/pais-ssh.new"
	chmod 755 "$bin_dir/pais-ssh.new"
	mv "$bin_dir/pais-ssh.new" "$bin_dir/pais-ssh"
	echo "Installed pais-ssh $("$bin_dir/pais-ssh" version) in $bin_dir"

	other=$(command -v pais-ssh || true)
	if [ -n "$other" ] && [ "$other" != "$bin_dir/pais-ssh" ]; then
		echo "Note: $other comes first on your PATH. Remove it, or ~/.ssh/config may keep using it."
	fi
	case ":$PATH:" in
	*":$bin_dir:"*) ;;
	*)
		case "${SHELL:-}" in
		*/zsh) rc="$HOME/.zshrc" ;;
		*/bash) rc="$HOME/.bashrc" ;;
		*) rc='your shell start-up file' ;;
		esac
		echo "$bin_dir is not on your PATH. Add this line to $rc and open a new terminal:"
		echo "  export PATH=\"\$HOME/.local/bin:\$PATH\""
		;;
	esac

	echo
	echo "Next: pais-ssh setup   (run it again after an upgrade, to rewrite its ~/.ssh/config block)"
}

fail() {
	echo "pais-ssh install: $*" >&2
	exit 1
}

main "$@"
