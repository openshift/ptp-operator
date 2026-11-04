#!/usr/bin/env bash
# Pipe Ginkgo -v output through this script to add START / DONE lines around each spec.
#
# Assumes reporter SilenceSkips=true: omits end-of-spec skip reports in DidRun only.
# At -v, Ginkgo still prints "[SKIPPED] in [It|BeforeEach|…] - file:line @ …" via
# EmitFailure while the spec runs; that line is suppressed here but still sets SKIPPED.
# Pending specs print "P [PENDING]"; that line is suppressed (status via infer_status_update).
#
# Ginkgo --- delimiters are suppressed; we print one after each "■ DONE" line.
# "▶ START" is printed only from release_queued_start (after "■ DONE" + delimiter).
# If the next spec headline arrives before the current spec finishes, queue it and wait.

set -euo pipefail

SPEC_SEP='━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━'

strip_ansi() {
	LC_ALL=C printf '%s' "$1" | LC_ALL=C sed -E $'s/\033\[[0-9;]*[A-Za-z]//g'
}

to_lower() {
	LC_ALL=C tr '[:upper:]' '[:lower:]' <<<"$1"
}

is_delimiter_line() {
	local p="$1"
	p="${p//$'\r'/}"
	p="${p#"${p%%[![:space:]]*}"}"
	p="${p%"${p##*[![:space:]]}"}"
	[[ ${#p} -ge 20 ]] || return 1
	[[ "$p" == *---* ]] || return 1
	[[ "$p" =~ ^[-[:space:]]+$ ]]
}

is_headline_line() {
	local plain="$1"
	local pl
	pl="$(to_lower "$plain")"
	if [[ "$pl" =~ \[serial\][[:space:]]+(passed|skipped|failed|pending)[[:space:]]*$ ]]; then
		return 1
	fi
	if [[ "$pl" =~ \[parallel\][[:space:]]+(passed|skipped|failed|pending)[[:space:]]*$ ]]; then
		return 1
	fi
	[[ "$pl" == *"[serial]"* || "$pl" == *"[parallel]"* ]] || return 1
	if [[ "$plain" =~ ^[[:space:]] ]]; then
		return 0
	fi
	case "$plain" in
	\[*) return 0 ;;
	esac
	return 1
}

is_ginkgo_emit_skip_line() {
	local plain="$1"
	[[ "$plain" =~ ^[[:space:]]*\[SKIPPED\][[:space:]]+in[[:space:]]+\[[^]]+\][[:space:]]*- ]]
}

is_ginkgo_emit_pending_line() {
	local plain="$1"
	[[ "$plain" =~ ^[[:space:]]*P[[:space:]]+\[PENDING\] ]]
}

infer_status_update() {
	local plain="$1"
	if [[ "$plain" == *'[FAILED]'* || "$plain" == *'FAIL!'* || "$plain" == *'--- FAIL:'* || "$plain" == *[Pp][Aa][Nn][Ii][Cc]:* ]]; then
		echo FAILED
		return
	fi
	if [[ "$plain" == *'[PENDING]'* || "$plain" == *' PENDING'* ]]; then
		echo PENDING
		return
	fi
	if [[ "$plain" == *'[SKIPPED]'* || "$plain" == *' SKIPPED'* ]]; then
		echo SKIPPED
		return
	fi
	if [[ "$plain" =~ ^[[:space:]]*•[[:space:]]+\[[0-9] ]]; then
		echo PASSED
		return
	fi
	echo ""
}

status_rank() {
	case "$1" in
	FAILED) echo 4 ;;
	PENDING) echo 3 ;;
	SKIPPED) echo 2 ;;
	PASSED) echo 1 ;;
	*) echo 0 ;;
	esac
}

# Bold bright-white for START/DONE labels; magenta for STEP labels (avoids
# clash with logrus INFO, typically cyan). Test names use bold dark orange
# (256-color 166) — readable on white and still visible on dark. Honors NO_COLOR.
bold_text() {
	local text="$1"
	if [[ -n "${NO_COLOR:-}" ]]; then
		printf '%s' "$text"
		return
	fi
	printf '\033[1;97m%s\033[0m' "$text"
}

name_colored() {
	local text="$1"
	if [[ -n "${NO_COLOR:-}" ]]; then
		printf '%s' "$text"
		return
	fi
	printf '\033[1;38;5;166m%s\033[0m' "$text"
}

status_colored() {
	local word="$1"
	if [[ -n "${NO_COLOR:-}" ]]; then
		printf '%s' "$word"
		return
	fi
	case "$word" in
	PASSED) printf '\033[1;32m%s\033[0m' "$word" ;;
	FAILED) printf '\033[1;31m%s\033[0m' "$word" ;;
	PENDING) printf '\033[1;33m%s\033[0m' "$word" ;;
	SKIPPED) printf '\033[1;34m%s\033[0m' "$word" ;;
	*) printf '\033[1m%s\033[0m' "$word" ;;
	esac
}

# Ginkgo By() lines look like: "  STEP: <content> @ <timestamp>"
is_step_line() {
	local plain="$1"
	[[ "$plain" =~ ^[[:space:]]*STEP:[[:space:]] ]]
}

print_step_line() {
	local plain="$1"
	if [[ -n "${NO_COLOR:-}" ]]; then
		printf '%s\n' "$plain"
		return
	fi
	# Keep indent, color "STEP:" magenta bold, bold the step content, dim timestamp.
	local indent content rest
	indent="${plain%%STEP:*}"
	rest="${plain#"${indent}STEP:"}"
	rest="${rest#"${rest%%[![:space:]]*}"}"
	if [[ "$rest" =~ ^(.*)[[:space:]]+@[[:space:]]+(.+)$ ]]; then
		content="${BASH_REMATCH[1]}"
		printf '%s\033[1;35mSTEP:\033[0m \033[1;97m%s\033[0m \033[90m@ %s\033[0m\n' \
			"$indent" "$content" "${BASH_REMATCH[2]}"
	else
		printf '%s\033[1;35mSTEP:\033[0m \033[1;97m%s\033[0m\n' "$indent" "$rest"
	fi
}

print_spec_start() {
	local name="$1"
	if [[ "$first_spec" == true ]]; then
		printf '%s\n%s\n%s\n' "${SPEC_SEP}" "${SPEC_SEP}" "${SPEC_SEP}"
		first_spec=false
	fi
	printf '🟢 '
	bold_text "START"
	printf '  '
	name_colored "$name"
	printf '\n'
}

release_queued_start() {
	if [[ -z "$queued_headline" ]]; then
		return
	fi
	spec_name="$queued_headline"
	queued_headline=""
	have_spec=true
	status=""
	print_spec_start "$spec_name"
}

print_spec_done() {
	local name="$1"
	local st="$2"
	done_icon='✅'
	case "$st" in
		FAILED)  done_icon='❌' ;;
		SKIPPED) done_icon='⏭️ ' ;;
		PENDING) done_icon='⏸️ ' ;;
	esac
	printf '%s ' "$done_icon"
	bold_text "DONE"
	printf '   '
	name_colored "$name"
	printf '  '
	status_colored "$st"
	printf '\n'
	printf '%s\n%s\n%s\n' "${SPEC_SEP}" "${SPEC_SEP}" "${SPEC_SEP}"
	release_queued_start
}

flush_spec_done() {
	if [[ "$have_spec" != true || -z "$status" ]]; then
		return 1
	fi
	local will_start_next=false
	[[ -n "$queued_headline" ]] && will_start_next=true
	print_spec_done "$spec_name" "$status"
	if [[ "$will_start_next" != true ]]; then
		have_spec=false
		spec_name=""
		status=""
	fi
	return 0
}

have_spec=false
first_spec=true
spec_name=""
status=""
queued_headline=""

while IFS= read -r line || [[ -n "${line}" ]]; do
	plain="$(strip_ansi "$line")"
	plain="${plain%$'\r'}"

	if is_delimiter_line "$plain"; then
		continue
	fi

	if is_headline_line "$plain"; then
		if [[ "$have_spec" == true && -z "$status" ]]; then
			queued_headline="$plain"
			continue
		fi
		flush_spec_done || true
		queued_headline="$plain"
		if [[ "$have_spec" != true ]]; then
			release_queued_start
		fi
		continue
	fi

	if [[ "$have_spec" == true ]]; then
		upd="$(infer_status_update "$plain")"
		if [[ -n "$upd" ]]; then
			if [[ -z "$status" ]] || (($(status_rank "$upd") > $(status_rank "$status"))); then
				status="$upd"
			fi
			if [[ -n "$queued_headline" ]]; then
				flush_spec_done || true
			fi
		fi
	fi

	if is_ginkgo_emit_skip_line "$plain" || is_ginkgo_emit_pending_line "$plain"; then
		continue
	fi
	if is_step_line "$plain"; then
		print_step_line "$plain"
		continue
	fi
	printf '%s\n' "$line"
done

flush_spec_done || true
