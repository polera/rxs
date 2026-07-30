#!/bin/sh
# Collect the license text of every module linked into an rxs binary.
#
# The module list is read from the binary itself via `go version -m` rather than
# `go list -deps`: the latter aborts partway through when cross-compiling,
# because modernc.org/libc has subpackages whose files are excluded by build
# constraints on non-Linux targets, and silently omits everything it never
# reached. The dependency set also genuinely varies by platform (x/termios on
# unix, x/windows on Windows), so run this once per build target.
#
# Usage: scripts/gen-licenses.sh <binary> [output-file]

set -eu

if [ $# -lt 1 ]; then
	echo "usage: $0 <binary> [output-file]" >&2
	exit 2
fi

binary="$1"
out="${2:-THIRD_PARTY_LICENSES.txt}"

if [ ! -f "$binary" ]; then
	echo "error: no such binary: $binary" >&2
	exit 1
fi

main_module="$(go list -m)"

work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

# "dep" lines are the modules actually linked in; "mod" is the main module and
# "=>" marks a replacement, neither of which we want to attribute here.
go version -m "$binary" |
	awk '$1 == "dep" && $2 != "" && $3 != "" { print $2 "@" $3 }' |
	sort -u >"$work/mods"

if [ ! -s "$work/mods" ]; then
	echo "error: no module metadata in $binary (built with -ldflags -buildid= or not a Go binary?)" >&2
	exit 1
fi

# Resolve each module to its extracted source directory in one batch.
# shellcheck disable=SC2046 # deliberate word splitting: one argument per module
go list -m -f '{{.Path}}	{{.Version}}	{{.Dir}}' $(tr '\n' ' ' <"$work/mods") >"$work/dirs"

body="$work/body"
: >"$body"
missing=0

while IFS='	' read -r path version dir; do
	[ -n "$path" ] || continue
	[ "$path" = "$main_module" ] && continue

	if [ -z "$dir" ] || [ ! -d "$dir" ]; then
		echo "error: $path $version is not extracted; run 'go mod download $path'" >&2
		missing=1
		continue
	fi

	find "$dir" -maxdepth 1 -type f \
		\( -iname 'LICENSE*' -o -iname 'LICENCE*' -o -iname 'COPYING*' -o -iname 'NOTICE*' \) |
		sort >"$work/files"

	if [ ! -s "$work/files" ]; then
		echo "error: no license file found for $path $version in $dir" >&2
		missing=1
		continue
	fi

	{
		echo
		echo "================================================================================"
		echo "$path $version"
		echo "================================================================================"
		while IFS= read -r f; do
			echo
			echo "--- $(basename "$f") ---"
			echo
			cat "$f"
		done <"$work/files"
	} >>"$body"
done <"$work/dirs"

if [ "$missing" -ne 0 ]; then
	echo "error: license collection incomplete; refusing to write $out" >&2
	exit 1
fi

count="$(grep -c '^================' "$body" | awk '{print $1 / 2}')"

{
	echo "Third-party licenses"
	echo
	echo "rxs ($main_module) is distributed under the MIT license; see LICENSE."
	echo "This binary statically links the $count modules below. Each license is"
	echo "reproduced in full to satisfy its attribution requirements."
	cat "$body"
} >"$out"

echo "wrote $out ($count modules)"
