#!/usr/bin/env bash
# Summarizes the failures in a "go test -v" log as annotations and a job summary.
# Usage: test-report.sh <log-file> <job-name>
set -u

log=$1
job=$2
summary=${GITHUB_STEP_SUMMARY:-/dev/null}

# Prints the output of each failed top-level test: everything from its "=== RUN" line up to its "--- FAIL" line.
failed_output=$(awk '
	{ lines[NR] = $0 }
	/^=== RUN / { start[$3] = NR }
	/^--- FAIL: / {
		name = $3
		from = (name in start) ? start[name] : NR
		print "### " name
		for (i = from + 1; i < NR; i++) {
			if (lines[i] !~ /^=== (RUN|PAUSE|CONT)/) print lines[i]
		}
		print ""
	}
' "$log")

failed_packages=$(grep -E '^(FAIL|panic:)' "$log" || true)

if [ -z "$failed_output" ] && [ -z "$failed_packages" ]; then
	exit 0
fi

{
	echo "## Test failures on $job"
	echo
	if [ -n "$failed_output" ]; then
		echo '```text'
		echo "$failed_output"
		echo '```'
	fi
	if [ -n "$failed_packages" ]; then
		echo '```text'
		echo "$failed_packages"
		echo '```'
	fi
} >>"$summary"

grep -E '^--- FAIL: ' "$log" | while read -r _ _ name _; do
	echo "::error title=Test failed on $job::$name"
done

echo "$failed_output"
echo "$failed_packages"
