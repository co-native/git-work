#!/usr/bin/env bash
#
# List known vulnerabilities across a Go module's full dependency graph
# (direct and indirect) with their CVSS base scores, and optionally upgrade
# past them.
#
# Data flow, and why it takes three sources:
#   go list -m -json all  -> every module in the build graph, with versions
#   api.osv.dev           -> which of those have known vulns (authoritative for Go)
#   GitHub advisories/NVD -> the numeric CVSS score, which OSV usually omits for
#                            Go advisories (it carries the vector at best)
#   govulncheck           -> whether your code actually reaches the vulnerable
#                            symbol, which matters far more than the base score
#
# Usage: check-dependency-vulns.sh [-d DIR] [-m SCORE] [-e] [-q]
#        check-dependency-vulns.sh --fix --min-score N [--fix-target T] [--yes]

set -euo pipefail

DIR="."
MIN_SCORE=""
EXIT_ON_FIND=0
QUIET=0
FIX=0
APPLY=0
FIX_TARGET="clean"

usage() {
	cat >&2 <<'USAGE'
Usage: check-dependency-vulns.sh [options]

Reporting:
  -d, --dir DIR         Go module directory to scan (default: current directory)
  -m, --min-score N     Only report vulns with a CVSS base score >= N (e.g. 7.0).
                        Vulns with no published score are never hidden by this
                        filter; they are listed separately as UNSCORED.
  -e, --exit-code       Exit 1 if any vuln meets the threshold (for CI)
  -q, --quiet           Suppress progress output on stderr
  -h, --help            Show this help

Upgrading:
      --fix             Plan upgrades past the reported vulns and print them.
                        Changes nothing without --yes. Requires --min-score.
      --yes             Actually run the planned upgrades, then tidy, build,
                        vet and rescan to confirm the findings cleared.
      --fix-target T    Which version to upgrade to:
                          clean    (default) lowest release with no known vulns
                          minimal  lowest release fixing the matched advisories

Every command that modifies the module or verifies it is echoed before it runs,
so --fix output is a copy-pasteable script and --fix --yes shows its work.

Only modules in your go.mod require list are candidates. Advisories against
modules that appear solely in the wider graph are reported but never "fixed":
they are not compiled into your binary, and `go get`ing them would add a
dependency you do not use.

Exit codes:
  0   ran successfully; nothing met the threshold (or -e was not given)
  1   with -e: findings at or above --min-score
  2   could not run: bad arguments, or a required tool is missing

Environment:
  NVD_API_KEY   Optional. Without it NVD allows ~5 requests per 30s, so this
                script sleeps 6s between NVD lookups. With a key, 1s.
USAGE
}

while [ $# -gt 0 ]; do
	case "$1" in
		-d|--dir)         DIR="${2:?--dir needs a value}"; shift 2 ;;
		-m|--min-score)   MIN_SCORE="${2:?--min-score needs a value}"; shift 2 ;;
		-e|--exit-code)   EXIT_ON_FIND=1; shift ;;
		-q|--quiet)       QUIET=1; shift ;;
		--fix)            FIX=1; shift ;;
		--yes)            APPLY=1; shift ;;
		--fix-target)     FIX_TARGET="${2:?--fix-target needs a value}"; shift 2 ;;
		-h|--help)        usage; exit 0 ;;
		*) echo "unknown argument: $1" >&2; usage; exit 2 ;;
	esac
done

if [ "$FIX" -eq 1 ] && [ -z "$MIN_SCORE" ]; then
	echo "--fix requires --min-score (e.g. --min-score 7.0)" >&2
	exit 2
fi
if [ "$APPLY" -eq 1 ] && [ "$FIX" -eq 0 ]; then
	echo "--yes is only meaningful with --fix" >&2
	exit 2
fi
case "$FIX_TARGET" in
	clean|minimal) ;;
	*) echo "--fix-target must be 'clean' or 'minimal'" >&2; exit 2 ;;
esac

# Exit 2, matching the argument errors above: callers gating on this script
# need "could not run" to be distinguishable from "found something".
for tool in go jq curl; do
	command -v "$tool" >/dev/null || { echo "required tool not found: $tool" >&2; exit 2; }
done

log() { [ "$QUIET" -eq 1 ] || echo "$*" >&2; }

# Echo a command exactly as it will run, then run it. Everything that mutates
# the module or verifies the result goes through this, so the transcript is
# reproducible by hand.
run() {
	echo "  \$ $*"
	"$@"
}

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT
mkdir -p "$TMP/osv"

cd "$DIR"

# ---------------------------------------------------------------- module graph
# `go list -m all` is the full graph, which is deliberately broader than go.mod's
# require list: it includes modules reached only through other modules' test and
# tooling dependencies. Those can carry advisories while never being linked into
# your binary, so the reachability column below is what separates the two.
scan() {
	log "==> Enumerating module graph"
	go list -m -json all 2>/dev/null \
		| jq -c 'select(.Main != true) | {package: {name: .Path, ecosystem: "Go"}, version: (.Version // empty)}' \
		| jq -s '{queries: .}' > "$TMP/query.json"

	MODCOUNT="$(jq '.queries | length' "$TMP/query.json")"
	log "    $MODCOUNT modules"

	# The require list from go.mod, a strict subset of the graph above. Used to
	# tell "declared dependency" from "reached only via someone else's
	# dependencies" without relying on govulncheck, whose view is the import graph.
	go mod edit -json | jq -r '.Require[]?.Path' | sort -u > "$TMP/required.txt"

	log "==> Querying osv.dev"
	curl -sS -X POST -d "@$TMP/query.json" https://api.osv.dev/v1/querybatch > "$TMP/result.json"

	# Pair each query back to its result: querybatch guarantees positional ordering.
	paste -d'\t' \
		<(jq -r '.queries[] | "\(.package.name)\t\(.version // "-")"' "$TMP/query.json") \
		<(jq -rc '.results[] | [.vulns[]?.id] | join(",")' "$TMP/result.json") \
		| awk -F'\t' '$3 != ""' > "$TMP/affected.tsv"

	# ------------------------------------------------------------- reachability
	# Optional: govulncheck is the only source here that knows whether the
	# vulnerable symbol is actually called. A high base score on an unreachable
	# path is noise.
	: > "$TMP/reachable.txt"
	: > "$TMP/present.txt"
	if command -v govulncheck >/dev/null; then
		log "==> Running govulncheck (reachability)"
		if govulncheck -format json ./... > "$TMP/gvc.json" 2>/dev/null; then
			jq -r 'select(.finding) | .finding.osv' "$TMP/gvc.json" 2>/dev/null \
				| sort -u > "$TMP/present.txt" || true
			jq -r 'select(.finding) | select(.finding.trace[0].function != null) | .finding.osv' \
				"$TMP/gvc.json" 2>/dev/null | sort -u > "$TMP/reachable.txt" || true
		else
			log "    govulncheck failed; skipping reachability"
		fi
	else
		log "==> govulncheck not installed; skipping reachability"
	fi

	log "==> Resolving CVSS scores"
	: > "$TMP/rows.tsv"
	[ -s "$TMP/affected.tsv" ] || return 0

	while IFS=$'\t' read -r mod version ids; do
		IFS=',' read -ra idlist <<< "$ids"
		for id in "${idlist[@]}"; do
			[ -s "$TMP/osv/$id.json" ] || \
				curl -sS "https://api.osv.dev/v1/vulns/$id" > "$TMP/osv/$id.json"
			local_json="$TMP/osv/$id.json"

			cve="$(jq -r '[.aliases[]? | select(startswith("CVE-"))] | first // ""' "$local_json")"
			summary="$(jq -r '.summary // .details // "-" | split("\n")[0]' "$local_json")"
			# Fixed version for *this* module, not every package the advisory
			# touches: Go advisories also list toolchain versions.
			fixed="$(jq -r --arg m "$mod" '
				[.affected[]? | select(.package.name == $m)
				 | .ranges[]?.events[]? | select(.fixed) | .fixed] | first // "-"' "$local_json")"

			read -r score sev src < <(lookup_score "$local_json" "$cve"; echo)
			score="${score:-}"; sev="${sev:-}"; src="${src:-}"

			reach="graph-only"
			grep -qx "$mod" "$TMP/required.txt" 2>/dev/null && reach="required"
			grep -qx "$id" "$TMP/present.txt" 2>/dev/null && reach="imported"
			grep -qx "$id" "$TMP/reachable.txt" 2>/dev/null && reach="REACHABLE"

			printf '%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n' \
				"${score:--}" "${cve:-$id}" "$mod" "$version" "$fixed" \
				"${sev:--}" "$reach" "${src:--}" "$id" "$summary" >> "$TMP/rows.tsv"
		done
	done < "$TMP/affected.tsv"
}

# --------------------------------------------------------------- score lookup
NVD_SLEEP=6
NVD_ARGS=()
if [ -n "${NVD_API_KEY:-}" ]; then
	NVD_SLEEP=1
	NVD_ARGS=(-H "apiKey: $NVD_API_KEY")
fi
NVD_CALLED=0

# Try OSV's own severity first (rare for Go, but free), then the GitHub advisory
# database, then NVD. Emits "score<TAB>severity<TAB>source" or nothing.
lookup_score() {
	local osv_json="$1" cve="$2" out

	out="$(jq -r '
		[.severity[]? | select(.type | test("CVSS")) | .score
		 | select(test("^[0-9.]+$"))] | first // empty' "$osv_json")"
	if [ -n "$out" ]; then printf '%s\t%s\tosv\n' "$out" "-"; return; fi

	[ -n "$cve" ] || return 0

	if command -v gh >/dev/null; then
		out="$(gh api "/advisories?cve_id=$cve" 2>/dev/null \
			| jq -r 'first(.[] | select((.cvss_severities.cvss_v3.score // .cvss.score) != null))
			         | [((.cvss_severities.cvss_v3.score // .cvss.score) | tostring), .severity]
			         | @tsv' 2>/dev/null)" || out=""
		if [ -n "$out" ]; then printf '%s\tghsa\n' "$out"; return; fi
	fi

	# NVD is rate limited hard without a key, so pay the sleep only when we
	# actually reach this branch.
	[ "$NVD_CALLED" -eq 0 ] || sleep "$NVD_SLEEP"
	NVD_CALLED=1
	out="$(curl -sS "${NVD_ARGS[@]}" \
		"https://services.nvd.nist.gov/rest/json/cves/2.0?cveId=$cve" 2>/dev/null \
		| jq -r 'first(.vulnerabilities[]?.cve.metrics
		         | (.cvssMetricV31[]?, .cvssMetricV30[]?, .cvssMetricV40[]?)
		         | .cvssData | [(.baseScore | tostring), .baseSeverity] | @tsv)' 2>/dev/null)" || out=""
	if [ -n "$out" ]; then printf '%s\tnvd\n' "$out"; return; fi
}

# --------------------------------------------------------------------- report
print_table() {
	printf '%-6s  %-18s  %-26s  %-10s  %-10s  %-11s  %s\n' \
		SCORE CVE MODULE HAVE FIXED REACHABILITY SUMMARY
	printf '%-6s  %-18s  %-26s  %-10s  %-10s  %-11s  %s\n' \
		"------" "------------------" "--------------------------" \
		"----------" "----------" "-----------" "-------"
	while IFS=$'\t' read -r score cve mod have fixed sev reach src id summary; do
		printf '%-6s  %-18s  %-26s  %-10s  %-10s  %-11s  %s\n' \
			"$score" "$cve" "$mod" "$have" "$fixed" "$reach" "$summary"
	done
}

report() {
	awk -F'\t' '$1 != "-"' "$TMP/rows.tsv" | sort -t$'\t' -k1,1gr > "$TMP/scored.tsv"
	awk -F'\t' '$1 == "-"' "$TMP/rows.tsv" > "$TMP/unscored.tsv"

	if [ -n "$MIN_SCORE" ]; then
		awk -F'\t' -v min="$MIN_SCORE" '$1 + 0 >= min + 0' "$TMP/scored.tsv" > "$TMP/f.tsv"
		mv "$TMP/f.tsv" "$TMP/scored.tsv"
		echo
		echo "Vulnerabilities with CVSS >= $MIN_SCORE across $MODCOUNT modules:"
	else
		echo
		echo "Known vulnerabilities across $MODCOUNT modules:"
	fi
	echo

	if [ -s "$TMP/scored.tsv" ]; then
		print_table < "$TMP/scored.tsv"
	else
		echo "  (none)"
	fi

	# Unscored vulns are reported regardless of the threshold: a missing score
	# means nobody has rated it yet, not that it is harmless. Silently dropping
	# these is how a threshold filter turns into a blind spot.
	if [ -s "$TMP/unscored.tsv" ]; then
		echo
		echo "UNSCORED (no CVSS published; not subject to the --min-score filter):"
		echo
		print_table < "$TMP/unscored.tsv"
	fi

	cat <<'LEGEND'

Reachability (increasing order of concern):
  graph-only   Not in go.mod. Reached only through another module's own
               dependencies and not linked into your binary. Usually noise.
  required     Declared in go.mod, but the vulnerable package is never imported
               by anything this module builds.
  imported     The vulnerable package is in your import graph, but govulncheck
               found no call path to the vulnerable symbol.
  REACHABLE    govulncheck found a call path to the vulnerable symbol. Act on these.
LEGEND
}

# ------------------------------------------------------------------- upgrading

# Index of $1 in the remaining arguments, or nothing. `go list -m -versions`
# emits versions in ascending semver order, so positions in that list are a
# usable ordering and we never have to compare semver in shell.
idx_of() {
	local want="$1"; shift
	local i=0 v
	for v in "$@"; do
		[ "$v" = "$want" ] && { echo "$i"; return 0; }
		i=$((i + 1))
	done
	return 1
}

# Lowest release of $mod that carries no known vulnerabilities at all. This is
# the right target when a policy gate (Nexus Firewall, Dependabot, an internal
# proxy) blocks on *any* advisory: landing on the minimum version that fixes
# today's CVE just gets you blocked again by the next one.
target_clean() {
	local mod="$1" cur="$2"
	local -a versions cands
	read -ra versions <<< "$(go list -m -versions "$mod" 2>/dev/null | cut -d' ' -f2-)"
	[ "${#versions[@]}" -gt 0 ] || return 1

	local curidx
	curidx="$(idx_of "$cur" "${versions[@]}")" || return 1
	cands=("${versions[@]:$((curidx + 1))}")
	[ "${#cands[@]}" -gt 0 ] || return 1

	# One batch query for every candidate rather than one call per version.
	local q="$TMP/cand-q.json" r="$TMP/cand-r.json" v
	: > "$TMP/cand.txt"
	for v in "${cands[@]}"; do echo "$v" >> "$TMP/cand.txt"; done
	jq -R -s --arg m "$mod" '
		{queries: (split("\n") | map(select(length > 0))
		 | map({package: {name: $m, ecosystem: "Go"}, version: .}))}' \
		"$TMP/cand.txt" > "$q"
	curl -sS -X POST -d "@$q" https://api.osv.dev/v1/querybatch > "$r" || return 1

	local i=0
	for v in "${cands[@]}"; do
		if [ "$(jq -r --argjson i "$i" '[.results[$i].vulns[]?] | length' "$r")" = "0" ]; then
			echo "$v"; return 0
		fi
		i=$((i + 1))
	done
	return 1
}

# Lowest release that fixes every matched advisory for $mod. Each advisory's
# `fixed` value is a point in the same ordered version list, so the answer is
# the furthest-along of them.
target_minimal() {
	local mod="$1" cur="$2"; shift 2
	local -a versions
	read -ra versions <<< "$(go list -m -versions "$mod" 2>/dev/null | cut -d' ' -f2-)"
	[ "${#versions[@]}" -gt 0 ] || return 1

	local best=-1 id fixed idx
	for id in "$@"; do
		fixed="$(jq -r --arg m "$mod" '
			[.affected[]? | select(.package.name == $m)
			 | .ranges[]?.events[]? | select(.fixed) | .fixed] | first // empty' \
			"$TMP/osv/$id.json")"
		[ -n "$fixed" ] || continue
		idx="$(idx_of "v${fixed#v}" "${versions[@]}")" || continue
		[ "$idx" -gt "$best" ] && best="$idx"
	done
	[ "$best" -ge 0 ] || return 1
	echo "${versions[$best]}"
}

go_directive() { go mod edit -json | jq -r '.Go // "-"'; }
require_snapshot() { go mod edit -json | jq -r '.Require[]? | "\(.Path) \(.Version)"' | sort; }

do_fix() {
	# Candidates are scored at or above the threshold AND declared in go.mod.
	# graph-only findings are excluded on purpose: they are not compiled into
	# the binary, and `go get`ing them writes a require line for a module this
	# project does not use, which `go mod tidy` then strips again.
	awk -F'\t' -v min="$MIN_SCORE" '$1 != "-" && $1 + 0 >= min + 0 && $7 != "graph-only"' \
		"$TMP/rows.tsv" > "$TMP/fixable.tsv"
	awk -F'\t' -v min="$MIN_SCORE" '$1 != "-" && $1 + 0 >= min + 0 && $7 == "graph-only"' \
		"$TMP/rows.tsv" > "$TMP/skipped.tsv"

	# Resolve every upgrade target up front, so the progress chatter this emits on
	# stderr cannot interleave into the middle of the report printed below.
	: > "$TMP/plan.tsv"
	: > "$TMP/warn.txt"
	if [ -s "$TMP/fixable.tsv" ]; then
		cut -f3 "$TMP/fixable.tsv" | sort -u > "$TMP/fixmods.txt"
		local mod have ids target
		while read -r mod; do
			have="$(awk -F'\t' -v m="$mod" '$3 == m {print $4; exit}' "$TMP/fixable.tsv")"
			ids="$(awk -F'\t' -v m="$mod" '$3 == m {print $9}' "$TMP/fixable.tsv" | tr '\n' ' ')"
			log "==> Resolving upgrade target for $mod"
			if [ "$FIX_TARGET" = "clean" ]; then
				target="$(target_clean "$mod" "$have")" || target=""
				if [ -z "$target" ]; then
					{
						echo "  ! $mod: no published release is free of known vulnerabilities;"
						echo "    falling back to the minimal fixing version."
					} >> "$TMP/warn.txt"
					target="$(target_minimal "$mod" "$have" $ids)" || target=""
				fi
			else
				target="$(target_minimal "$mod" "$have" $ids)" || target=""
			fi
			if [ -z "$target" ]; then
				echo "  ! $mod@$have: could not determine an upgrade target; skipping." \
					>> "$TMP/warn.txt"
				continue
			fi
			printf '%s\t%s\t%s\t%s\n' "$mod" "$have" "$target" "$ids" >> "$TMP/plan.tsv"
		done < "$TMP/fixmods.txt"
	fi

	echo
	echo "=============================================================="
	echo "FIX PLAN  (target: $FIX_TARGET, threshold: CVSS >= $MIN_SCORE)"
	echo "=============================================================="
	echo

	if [ -s "$TMP/skipped.tsv" ]; then
		echo "Skipped (not in go.mod; present only in the wider module graph, so"
		echo "not compiled into your binary and not fixable by upgrading):"
		while IFS=$'\t' read -r score cve mod have fixed sev reach src id summary; do
			printf '  %-6s %-18s %s@%s\n' "$score" "$cve" "$mod" "$have"
		done < "$TMP/skipped.tsv"
		echo
	fi

	local unscored_n
	unscored_n="$(wc -l < "$TMP/unscored.tsv" | tr -d ' ')"
	if [ "$unscored_n" -gt 0 ]; then
		echo "Note: $unscored_n advisory/advisories have no published CVSS score and"
		echo "      therefore cannot meet the threshold. They are listed above but"
		echo "      will not be fixed. Review them by hand."
		echo
	fi

	[ -s "$TMP/warn.txt" ] && { cat "$TMP/warn.txt"; echo; }

	if [ ! -s "$TMP/fixable.tsv" ]; then
		echo "Nothing to upgrade."
		return 0
	fi
	if [ ! -s "$TMP/plan.tsv" ]; then
		echo "No upgrade targets could be resolved."
		return 0
	fi

	echo "Planned upgrades:"
	echo
	printf '  %-30s %-12s %-12s %s\n' MODULE FROM TO CLEARS
	printf '  %-30s %-12s %-12s %s\n' "------" "----" "--" "------"
	while IFS=$'\t' read -r mod have target ids; do
		local cves
		cves="$(awk -F'\t' -v m="$mod" '$3 == m {print $2}' "$TMP/fixable.tsv" | tr '\n' ' ')"
		printf '  %-30s %-12s %-12s %s\n' "$mod" "$have" "$target" "$cves"
	done < "$TMP/plan.tsv"

	echo
	echo "Commands:"
	while IFS=$'\t' read -r mod have target ids; do
		echo "  \$ go get $mod@$target"
	done < "$TMP/plan.tsv"
	echo "  \$ go mod tidy"

	if [ "$APPLY" -eq 0 ]; then
		echo
		echo "Planning only. Re-run with --yes to apply."
		return 0
	fi

	# --------------------------------------------------------------- executing
	echo
	echo "=============================================================="
	echo "APPLYING"
	echo "=============================================================="
	echo

	# Snapshot for rollback. Deliberately file copies rather than git: this has
	# to work on a dirty tree and must not touch anything else the user staged.
	cp go.mod "$TMP/go.mod.bak"
	[ -f go.sum ] && cp go.sum "$TMP/go.sum.bak"
	local go_before require_before
	go_before="$(go_directive)"
	require_snapshot > "$TMP/require.before"

	rollback() {
		echo
		echo "Rolling back go.mod/go.sum to their pre-upgrade contents."
		cp "$TMP/go.mod.bak" go.mod
		[ -f "$TMP/go.sum.bak" ] && cp "$TMP/go.sum.bak" go.sum
	}

	while IFS=$'\t' read -r mod have target ids; do
		run go get "$mod@$target" || { rollback; echo "go get failed" >&2; return 1; }
	done < "$TMP/plan.tsv"
	run go mod tidy || { rollback; echo "go mod tidy failed" >&2; return 1; }

	# An upgrade can move the go directive as a side effect, raising the minimum
	# Go version for everyone building this module. Permitted, but never silent:
	# the require diff below does not cover the directive, so report it here.
	local go_after
	go_after="$(go_directive)"
	if [ "$go_after" != "$go_before" ]; then
		echo
		echo "  ! go directive raised $go_before -> $go_after"
		echo "    This raises the minimum Go version for consumers of this module."
	fi

	# Report everything that moved, not just what was targeted: upgrades drag
	# other requirements up with them via MVS.
	require_snapshot > "$TMP/require.after"
	echo
	echo "All go.mod requirement changes:"
	join -j 1 -o 0,1.2,2.2 <(awk '{print $1, $2}' "$TMP/require.before" | sort) \
	                       <(awk '{print $1, $2}' "$TMP/require.after" | sort) 2>/dev/null \
		| awk '$2 != $3 {printf "  %-34s %-12s -> %s\n", $1, $2, $3}'
	comm -13 <(cut -d' ' -f1 "$TMP/require.before") <(cut -d' ' -f1 "$TMP/require.after") \
		| sed 's/^/  ADDED   /'
	comm -23 <(cut -d' ' -f1 "$TMP/require.before") <(cut -d' ' -f1 "$TMP/require.after") \
		| sed 's/^/  REMOVED /'

	echo
	echo "Verifying:"
	run go build ./... || { echo "build failed after upgrade" >&2; return 1; }
	run go vet ./... || { echo "vet failed after upgrade" >&2; return 1; }

	echo
	echo "Rescanning to confirm the findings cleared:"
	run "$0" -d "$DIR" -q -m "$MIN_SCORE" || true
}

# ----------------------------------------------------------------------- main
scan

if [ ! -s "$TMP/rows.tsv" ]; then
	echo "No known vulnerabilities across ${MODCOUNT:-0} modules."
	exit 0
fi

report

if [ "$FIX" -eq 1 ]; then
	do_fix
fi

if [ "$EXIT_ON_FIND" -eq 1 ] && [ -s "$TMP/scored.tsv" ]; then
	exit 1
fi
