#!/bin/sh
# Verify that every dependency version in go.mod has been license-reviewed.

set -eu

policy="${LICENSE_POLICY:-licenses/approved-modules.txt}"

if [ ! -f "$policy" ]; then
	echo "error: license policy not found: $policy" >&2
	exit 1
fi

work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

# Extract both block and single-line require directives without adding a JSON
# parser as a CI dependency.
awk '
	$1 == "require" && $2 == "(" { in_require = 1; next }
	in_require && $1 == ")" { in_require = 0; next }
	in_require && $1 !~ /^\/\// && NF >= 2 { print $1 "@" $2; next }
	$1 == "require" && $2 != "(" && NF >= 3 { print $2 "@" $3 }
' go.mod | sort -u >"$work/required"

awk 'NF && $1 !~ /^#/ { print $1 }' "$policy" | sort >"$work/reviewed"
sort -u "$work/reviewed" >"$work/reviewed-unique"

if ! cmp -s "$work/reviewed" "$work/reviewed-unique"; then
	echo "error: duplicate modules in $policy" >&2
	diff -u "$work/reviewed-unique" "$work/reviewed" >&2 || true
	exit 1
fi

invalid=0
while read -r module license_expression; do
	[ -n "$module" ] || continue
	case "$module" in
		\#*) continue ;;
	esac

	case "$license_expression" in
		MIT | BSD-2-Clause | BSD-3-Clause | LicenseRef-Public-Domain) ;;
		"BSD-3-Clause AND MIT AND BSD-2-Clause AND LicenseRef-Public-Domain") ;;
		*)
			echo "error: unapproved license expression for $module: $license_expression" >&2
			invalid=1
			;;
	esac
done <"$policy"

if [ "$invalid" -ne 0 ]; then
	exit 1
fi

if ! cmp -s "$work/required" "$work/reviewed"; then
	echo "error: $policy does not match the dependencies pinned in go.mod" >&2
	diff -u "$work/reviewed" "$work/required" >&2 || true
	exit 1
fi

count="$(wc -l <"$work/required" | tr -d ' ')"
echo "license policy covers $count dependencies"
