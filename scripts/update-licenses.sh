#!/bin/sh
# Update reviewed module versions from go.mod without changing license approvals.

set -eu

policy="${LICENSE_POLICY:-licenses/approved-modules.txt}"

if [ ! -f "$policy" ]; then
	echo "error: license policy not found: $policy" >&2
	exit 1
fi

work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

awk '
	FNR == NR {
		if ($1 ~ /^#/ || NF == 0) {
			next
		}
		module = $1
		sub(/@[^@]+$/, "", module)
		expression = $0
		sub(/^[^[:space:]]+[[:space:]]+/, "", expression)
		license[module] = expression
		next
	}

	function emit(module, version) {
		if (!(module in license)) {
			print "error: no reviewed license for " module "@" version > "/dev/stderr"
			missing = 1
			return
		}
		print module "@" version " " license[module]
	}

	$1 == "require" && $2 == "(" { in_require = 1; next }
	in_require && $1 == ")" { in_require = 0; next }
	in_require && $1 !~ /^\/\// && NF >= 2 { emit($1, $2); next }
	$1 == "require" && $2 != "(" && NF >= 3 { emit($2, $3) }

	END { exit missing }
' "$policy" go.mod >"$work/entries-unsorted"

LC_ALL=C sort -u "$work/entries-unsorted" >"$work/entries"

awk '
	$1 !~ /^#/ && NF != 0 { exit }
	{ print }
' "$policy" >"$work/header"

awk '{ print }' "$work/header" "$work/entries" >"$work/policy"

mv "$work/policy" "$policy"
echo "updated $policy"
