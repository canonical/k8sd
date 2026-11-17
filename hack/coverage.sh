#!/bin/bash
set -euo pipefail
export LC_ALL=C

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "${SCRIPT_DIR}/.." && pwd)"

PROFILE="coverage.txt"
BASE_BREAKDOWN=""
MARKDOWN_OUT="coverage.md"
FILTERED_PROFILE="coverage.filtered.txt"
BREAKDOWN_OUT="coverage.breakdown.tsv"

usage() {
    echo "Usage: $0 [-p profile] [-b base-breakdown.tsv] [-o markdown-out]" >&2
    exit 2
}

while getopts ":p:b:o:h" opt; do
    case "${opt}" in
        p) PROFILE="${OPTARG}" ;;
        b) BASE_BREAKDOWN="${OPTARG}" ;;
        o) MARKDOWN_OUT="${OPTARG}" ;;
        h) usage ;;
        \?) echo "Error: unknown option -${OPTARG}" >&2; usage ;;
        :) echo "Error: option -${OPTARG} requires an argument" >&2; usage ;;
    esac
done
shift $((OPTIND - 1))

if [[ ! -f "${PROFILE}" ]]; then
    echo "Error: profile file '${PROFILE}' not found" >&2
    exit 1
fi
if [[ -n "${BASE_BREAKDOWN}" && ! -f "${BASE_BREAKDOWN}" ]]; then
    echo "Notice: base breakdown file '${BASE_BREAKDOWN}' not found, skipping base diff" >&2
    BASE_BREAKDOWN=""
fi

# Load configuration with env override taking precedence
ENV_THRESHOLD="${COVERAGE_THRESHOLD:-}"
CONF_FILE="${SCRIPT_DIR}/coverage.conf"

COVERAGE_THRESHOLD=0
COVERAGE_EXCLUDE=()

if [[ -f "${CONF_FILE}" ]]; then
    # shellcheck source=/dev/null
    . "${CONF_FILE}"
fi

if [[ -n "${ENV_THRESHOLD}" ]]; then
    COVERAGE_THRESHOLD="${ENV_THRESHOLD}"
fi

# Validate COVERAGE_THRESHOLD format
if [[ ! "${COVERAGE_THRESHOLD}" =~ ^[0-9]+(\.[0-9]+)?$ ]]; then
    echo "Error: COVERAGE_THRESHOLD '${COVERAGE_THRESHOLD}' is not a valid number (expected e.g. 46.7)" >&2
    exit 1
fi

# Find module prefix from go.mod
GO_MOD="${ROOT_DIR}/go.mod"
if [[ ! -f "${GO_MOD}" ]]; then
    echo "Error: go.mod not found at '${GO_MOD}'" >&2
    exit 1
fi

MODULE="$(awk '/^module / {print $2}' "${GO_MOD}")"
if [[ -z "${MODULE}" ]]; then
    echo "Error: unable to determine module name from '${GO_MOD}'" >&2
    exit 1
fi
PREFIX="${MODULE}/"

# Build combined ERE pattern from COVERAGE_EXCLUDE
EXCLUDE_REGEX=""
for pat in "${COVERAGE_EXCLUDE[@]}"; do
    if [[ -z "${EXCLUDE_REGEX}" ]]; then
        EXCLUDE_REGEX="(${pat})"
    else
        EXCLUDE_REGEX="${EXCLUDE_REGEX}|(${pat})"
    fi
done

# Run awk processor
EXCLUDE_REGEX="${EXCLUDE_REGEX}" awk \
  -v prefix="${PREFIX}" \
  -v filtered_out="${FILTERED_PROFILE}" \
  -v breakdown_out="${BREAKDOWN_OUT}" \
  -v markdown_out="${MARKDOWN_OUT}" \
  -v base_file="${BASE_BREAKDOWN}" \
  -v threshold="${COVERAGE_THRESHOLD}" \
  -v profile_name="${PROFILE}" '
function clamp_delta(d) {
    if (d > -0.005 && d < 0.005) {
        return 0
    }
    return d
}

BEGIN {
    exclude = ENVIRON["EXCLUDE_REGEX"]
    # Read base breakdown if supplied and accessible
    has_base = 0
    num_base_pkgs = 0
    if (base_file != "") {
        while ((getline line < base_file) > 0) {
            n = split(line, flds, "\t")
            if (n >= 3 && flds[2] ~ /^[0-9]+$/ && flds[3] ~ /^[0-9]+$/) {
                bpkg = flds[1]
                bcov = flds[2] + 0
                btot = flds[3] + 0
                if (bpkg == "TOTAL") {
                    base_total_cov = bcov
                    base_total_tot = btot
                    has_base = 1
                } else {
                    base_pkg_cov[bpkg] = bcov
                    base_pkg_tot[bpkg] = btot
                    base_pkgs[++num_base_pkgs] = bpkg
                }
            }
        }
        close(base_file)
    }
    seen_mode = 0
}
/^mode:/ {
    if (!seen_mode) {
        print $0 > filtered_out
        seen_mode = 1
    }
    next
}
NF == 3 {
    loc = $1
    stmts = $2 + 0
    cnt = $3 + 0

    # Strip module prefix
    relpath = loc
    if (index(relpath, prefix) == 1) {
        relpath = substr(relpath, length(prefix) + 1)
    }

    # Extract file path
    split(relpath, pparts, ":")
    file_path = pparts[1]

    # Check exclude regex
    if (exclude != "" && file_path ~ exclude) {
        next
    }

    # Write original line to filtered profile
    print $0 > filtered_out

    # Deduplicate block by loc
    if (!(loc in block_stmts)) {
        block_stmts[loc] = stmts
        block_cov[loc] = (cnt > 0 ? 1 : 0)

        # Determine package directory
        n = split(file_path, dparts, "/")
        pkg = ""
        for (i = 1; i < n; i++) {
            pkg = (pkg == "") ? dparts[i] : pkg "/" dparts[i]
        }
        if (pkg == "") pkg = "."
        block_pkg[loc] = pkg
    } else {
        if (cnt > 0) {
            block_cov[loc] = 1
        }
    }
}
END {
    if (!seen_mode) {
        printf "Error: profile \x27%s\x27 has no mode: header\n", profile_name > "/dev/stderr"
        exit 1
    }

    # Aggregate head package statistics
    for (loc in block_stmts) {
        pkg = block_pkg[loc]
        pkg_total[pkg] += block_stmts[loc]
        if (block_cov[loc]) {
            pkg_cov[pkg] += block_stmts[loc]
        }
        all_pkgs[pkg] = 1
        in_head[pkg] = 1
        total_stmts += block_stmts[loc]
        if (block_cov[loc]) {
            total_cov += block_stmts[loc]
        }
    }

    if (has_base) {
        for (i = 1; i <= num_base_pkgs; i++) {
            all_pkgs[base_pkgs[i]] = 1
        }
    }

    total_pct = (total_stmts > 0 ? (total_cov * 100 / total_stmts) : 0)

    # Write breakdown TSV (alphabetical by package name)
    num_pkgs = 0
    for (pkg in in_head) {
        head_pkg_list[++num_pkgs] = pkg
    }
    for (i = 1; i <= num_pkgs; i++) {
        for (j = i + 1; j <= num_pkgs; j++) {
            if (head_pkg_list[i] > head_pkg_list[j]) {
                tmp = head_pkg_list[i]
                head_pkg_list[i] = head_pkg_list[j]
                head_pkg_list[j] = tmp
            }
        }
    }
    for (i = 1; i <= num_pkgs; i++) {
        p = head_pkg_list[i]
        printf "%s\t%d\t%d\n", p, pkg_cov[p] + 0, pkg_total[p] + 0 > breakdown_out
    }
    printf "TOTAL\t%d\t%d\n", total_cov + 0, total_stmts + 0 > breakdown_out
    close(breakdown_out)
    close(filtered_out)

    # Sort all packages by uncovered statements descending, then by package name ascending
    total_all = 0
    for (pkg in all_pkgs) {
        sort_pkgs[++total_all] = pkg
        uncovered[pkg] = (pkg in in_head) ? (pkg_total[pkg] - pkg_cov[pkg]) : 0
    }
    for (i = 1; i <= total_all; i++) {
        for (j = i + 1; j <= total_all; j++) {
            p1 = sort_pkgs[i]
            p2 = sort_pkgs[j]
            swap = 0
            if (uncovered[p1] < uncovered[p2]) {
                swap = 1
            } else if (uncovered[p1] == uncovered[p2] && p1 > p2) {
                swap = 1
            }
            if (swap) {
                tmp = sort_pkgs[i]
                sort_pkgs[i] = sort_pkgs[j]
                sort_pkgs[j] = tmp
            }
        }
    }

    # Generate Markdown report
    md = "### Go unit test coverage\n\n"
    md = md "Isolated per-package unit coverage (no -coverpkg) with exclusions configured in hack/coverage.conf.\n\n"

    if (has_base) {
        base_pct = (base_total_tot > 0 ? (base_total_cov * 100 / base_total_tot) : 0)
        delta = clamp_delta(total_pct - base_pct)
        delta_sign = (delta >= 0 ? "+" : "")
        md = md sprintf("Total coverage: **%.2f%%** (base: **%.2f%%**, **%s%.2f pp**, threshold: **%s%%**)\n\n", \
            total_pct, base_pct, delta_sign, delta, threshold)
    } else {
        md = md sprintf("Total coverage: **%.2f%%** (threshold: **%s%%**)\n\n", total_pct, threshold)
    }

    md = md "<details>\n<summary>Package breakdown</summary>\n\n"
    if (has_base) {
        md = md "| Package | Coverage | Covered / Total | Δ vs base |\n"
        md = md "|:---|---:|---:|---:|\n"
        for (i = 1; i <= total_all; i++) {
            p = sort_pkgs[i]
            in_h = (p in in_head)
            in_b = (p in base_pkg_tot)
            if (in_h && in_b) {
                cov = pkg_cov[p] + 0
                tot = pkg_total[p] + 0
                cpct = (tot > 0 ? (cov * 100 / tot) : 0)
                bcov = base_pkg_cov[p] + 0
                btot = base_pkg_tot[p] + 0
                bpct = (btot > 0 ? (bcov * 100 / btot) : 0)
                pdelta = clamp_delta(cpct - bpct)
                pdelta_str = sprintf("%s%.2f pp", (pdelta >= 0 ? "+" : ""), pdelta)
                md = md sprintf("| %s | %.2f%% | %d / %d | %s |\n", p, cpct, cov, tot, pdelta_str)
            } else if (in_h) {
                cov = pkg_cov[p] + 0
                tot = pkg_total[p] + 0
                cpct = (tot > 0 ? (cov * 100 / tot) : 0)
                md = md sprintf("| %s | %.2f%% | %d / %d | new |\n", p, cpct, cov, tot)
            } else {
                md = md sprintf("| %s | — | — | removed |\n", p)
            }
        }
    } else {
        md = md "| Package | Coverage | Covered / Total |\n"
        md = md "|:---|---:|---:|\n"
        for (i = 1; i <= total_all; i++) {
            p = sort_pkgs[i]
            cov = pkg_cov[p] + 0
            tot = pkg_total[p] + 0
            cpct = (tot > 0 ? (cov * 100 / tot) : 0)
            md = md sprintf("| %s | %.2f%% | %d / %d |\n", p, cpct, cov, tot)
        }
    }
    md = md "</details>\n"

    printf "%s", md > markdown_out
    close(markdown_out)

    # Print markdown to stdout
    printf "%s", md

    # Gate check with epsilon
    thresh_val = threshold + 0
    if (total_pct + 1e-9 < thresh_val) {
        printf "\nCoverage gate FAILED: %.2f%% is below threshold %s%%\n", total_pct, threshold > "/dev/stderr"
        exit 1
    } else {
        printf "\nCoverage gate PASSED: %.2f%% meets threshold %s%%\n", total_pct, threshold
        exit 0
    }
}' "${PROFILE}"
