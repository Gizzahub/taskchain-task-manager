#!/bin/bash
# parity.sh: CE 패리티 fixture 러너
# 용도: `ce task` 참조 동작(고정 커밋 bb970b24)을 fixture expected로 고정하고(capture),
#       같은 실행으로 미세차(diff)를 검증하며(verify), 제품 바이너리가 이 계약에서
#       얼마나 떨어져 있는지 묶음별 pass/fail로 측정한다(report).
# 사용법: scripts/parity.sh capture|verify|report [bundle/fixture ...]
#   환경변수:
#     CE_BIN        참조 바이너리(기본 ce). 빌드 문자열이 PARITY_PIN을 포함해야 한다
#     PARITY_PIN    필수 빌드 접미(기본 bb970b24). 'unpinned'이면 검사 생략
#     PARITY_KEEP   1이면 마지막 실행 workspace를 build/parity-workspace에 보존
#     PRODUCT_BIN   report 모드에서 측정할 제품 바이너리 경로(위치인자는 전부
#                   fixture selector로 읽힌다)
#   종료코드: verify는 전부 통과면 0, 하나라도 미세차면 1, 사용법·pin 오류는 2.
#            capture와 report는 기록 완료 시 0(실패는 보고서 안에 기록된다).

set -euo pipefail

usage() {
	printf 'usage: %s capture|verify|report [bundle/fixture ...]\n' "$0" >&2
	exit 2
}

[ $# -ge 1 ] || usage
MODE="$1"
shift

case "$MODE" in
capture | verify | report) ;;
*) usage ;;
esac

SELF_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SUITE_DIR="$(cd "$SELF_DIR/.." && pwd)"
CE_BIN="${CE_BIN:-ce}"
PARITY_PIN="${PARITY_PIN:-bb970b24}"
PARITY_KEEP="${PARITY_KEEP:-0}"
PRODUCT_BIN="${PRODUCT_BIN:-}"

SCRATCH_ROOT="$(mktemp -d "${TMPDIR:-/tmp}/ce-parity.XXXXXX")"
LAST_WORKSPACE=""
cleanup() {
	if [ "$PARITY_KEEP" = 1 ] && [ -n "$LAST_WORKSPACE" ] && [ -d "$LAST_WORKSPACE" ]; then
		local dest
		dest="$(pwd)/build/parity-workspace"
		mkdir -p build
		rm -rf "$dest"
		cp -R "$LAST_WORKSPACE" "$dest"
		printf 'parity: 마지막 workspace 보존: %s\n' "$dest" >&2
	fi
	rm -rf "$SCRATCH_ROOT"
}
trap cleanup EXIT

fail() {
	printf 'parity: %s\n' "$*" >&2
	exit 2
}

hash_stream() {
	if command -v shasum >/dev/null 2>&1; then
		shasum -a 256 | cut -d' ' -f1
	else
		sha256sum | cut -d' ' -f1
	fi
}

# --- fixture 탐색 -------------------------------------------------------------
# fixture 디렉터리: fixtures/ce-parity/<bundle>/<name>/fixture.sh

list_fixtures() {
	local bundle_dir fx_dir
	for bundle_dir in "$SUITE_DIR"/*/; do
		[ -d "$bundle_dir" ] || continue
		for fx_dir in "$bundle_dir"*/; do
			[ -f "$fx_dir/fixture.sh" ] || continue
			printf '%s/%s\n' "$(basename "$bundle_dir")" "$(basename "$fx_dir")"
		done
	done
}

select_fixtures() {
	local selector fx found
	if [ $# -eq 0 ]; then
		list_fixtures
		return 0
	fi
	# fail()이 파이프라인 안에 있으면 하위 셸만 죽고 스크립트는 0으로 계속된다.
	# 검증은 파이프라인 밖에서 하고, 수집이 끝난 뒤에 정렬한다.
	local -a matched=()
	for selector in "$@"; do
		found=0
		while IFS= read -r fx; do
			case "$fx" in
			*"$selector"*) matched+=("$fx"); found=1 ;;
			esac
		done < <(list_fixtures)
		[ "$found" = 1 ] || fail "selector 와 일치하는 fixture 가 없습니다: $selector"
	done
	if [ "${#matched[@]}" -gt 0 ]; then
		printf '%s\n' ${matched[@]+"${matched[@]}"} | LC_ALL=C sort -u
	fi
}

# --- 정규화 -------------------------------------------------------------------
# 캡처와 비교 양쪽에 같은 프로그램을 적용한다. 정규화기는 "무엇을 parity로 보지
# 않을 것인가"의 선언이다: 실행마다 달라지는 것만 상수로 바꾼다.

write_normalizer_program() {
	local out_file="$1" ws_path="$2"
	# CE는 경로를 Go filepath.Clean으로 출력하므로 mktemp 템플릿이 남긴 이중
	# 슬래시를 규칙 쪽에서 먼저 접어야 같은 형태가 된다. 패턴을 반드시
	# 이스케이프형으로 쓴다 — 따옴표로 묶은 '//' 패턴은 bash 3.2에서 리터럴로
	# 읽혀 아무것도 바꾸지 않는다.
	ws_path="${ws_path//\/\///}"
	{
		printf 's|%s|<WS>|g\n' "$ws_path"
		printf 's/^created: [0-9]{4}-[0-9]{2}-[0-9]{2}$/created: <DATE>/\n'
		printf 's/^created-at: [0-9]{4}-[0-9]{2}-[0-9]{2}$/created-at: <DATE>/\n'
		printf 's/^resolved-at: [0-9].*/resolved-at: <TIMESTAMP>/\n'
		printf 's/^quality-reviewed-at: .*/quality-reviewed-at: <DATE>/\n'
	} >"$out_file"
}

normalize_file() {
	local norm_prog="$1" file="$2" out="$3"
	sed -E -f "$norm_prog" "$file" >"$out"
}

# --- fixture 실행 ---------------------------------------------------------------
# 한 fixture를 <bin>으로 실행하고 정규화된 산출물을 out_dir에 쓴다.
# out_dir: stdout, stderr, exit-code, tree.txt

run_fixture() {
	local fx_dir="$1" out_dir="$2" bin="$3" ws_path="$4"
	local norm_prog
	norm_prog="$SCRATCH_ROOT/normalize.sed"
	write_normalizer_program "$norm_prog" "$ws_path"

	# shellcheck source=/dev/null
	local PARITY_CMD=() PARITY_TASKS_DIR="" PARITY_SNAP_GIT_CE=0 PARITY_SETUP=""
	. "$fx_dir/fixture.sh"

	mkdir -p "$out_dir"
	local ws="$ws_path"
	rm -rf "$ws"
	mkdir -p "$ws"
	if [ -d "$fx_dir/board" ]; then
		cp -R "$fx_dir/board/." "$ws/"
	fi
	git -C "$ws" init -q
	git -C "$ws" config user.email parity@example.invalid
	git -C "$ws" config user.name parity
	git -C "$ws" add -A
	GIT_AUTHOR_DATE='2026-09-29T00:00:00Z' GIT_COMMITTER_DATE='2026-09-29T00:00:00Z' \
		git -C "$ws" commit -qm 'parity fixture board' >/dev/null 2>&1 || true
	if [ -n "$PARITY_SETUP" ]; then
		(cd "$ws" && eval "$PARITY_SETUP") || fail "setup 실패: $fx_dir"
	fi

	local env_prefix=()
	[ -n "$PARITY_TASKS_DIR" ] && env_prefix=(TASKS_DIR="$PARITY_TASKS_DIR")

	set +e
	(
		cd "$ws" || exit 97
		# </dev/null: 측정 대상이 stdin을 읽어도 이 루프의 fixture 목록을
		# 소진하지 않도록 떼어 놓는다.
		env ${env_prefix[@]+"${env_prefix[@]}"} "$bin" task ${PARITY_CMD[@]+"${PARITY_CMD[@]}"} \
			>"$out_dir/stdout.raw" 2>"$out_dir/stderr.raw" </dev/null
	)
	local rc=$?
	set -e
	printf '%s\n' "$rc" >"$out_dir/exit-code"

	normalize_file "$norm_prog" "$out_dir/stdout.raw" "$out_dir/stdout"
	normalize_file "$norm_prog" "$out_dir/stderr.raw" "$out_dir/stderr"
	rm -f "$out_dir/stdout.raw" "$out_dir/stderr.raw"

	# 결과 트리: workspace 전체 파일(내용은 같은 정규화 적용)의 해시 목록.
	# .git은 제외하되 PARITY_SNAP_GIT_CE=1이면 .git/ce/**는 포함한다 — 카드 ID
	# 원장이 git common dir 안에 살기 때문이다.
	local tree_list
	tree_list="$SCRATCH_ROOT/tree.list"
	(cd "$ws" && find . -type f | sed 's|^\./||' | LC_ALL=C sort) >"$tree_list"
	: >"$out_dir/tree.txt"
	local f include
	while IFS= read -r f; do
		include=1
		case "$f" in
		.git/*)
			include=0
			if [ "$PARITY_SNAP_GIT_CE" = 1 ]; then
				case "$f" in
				.git/ce/*) include=1 ;;
				esac
			fi
			;;
		esac
		[ "$include" = 1 ] || continue
		printf '%s  %s\n' "$(sed -E -f "$norm_prog" "$ws/$f" | hash_stream)" "$f" >>"$out_dir/tree.txt"
	done <"$tree_list"
}

pin_check() {
	[ "$PARITY_PIN" = "unpinned" ] && return 0
	local v
	v="$("$CE_BIN" version 2>&1 | tr '\n' ' ')" || fail "CE_BIN=$CE_BIN 실행 불가"
	case "$v" in
	*"$PARITY_PIN"*) : ;;
	*) fail "CE 빌드가 pin $PARITY_PIN 와 일치하지 않습니다: $v (다른 기준값에 캡처한 fixture를 망가뜨리지 않도록 중단합니다. CE_BIN·PARITY_PIN 으로 기준을 명시하세요)" ;;
	esac
}

FIRST_DIFF_REASON=""

first_diff() {
	local label="$1" exp="$2" act="$3"
	if [ ! -f "$exp" ] || [ ! -f "$act" ]; then
		FIRST_DIFF_REASON="$label 파일 없음 (expected: $([ -f "$exp" ] && echo 있음 || echo 없음), actual: $([ -f "$act" ] && echo 있음 || echo 없음))"
		return 0
	fi
	if [ "$label" = "exit-code" ]; then
		if ! cmp -s "$exp" "$act"; then
			FIRST_DIFF_REASON="exit-code $(cat "$exp" 2>/dev/null | tr -d '[:space:]') != $(cat "$act" 2>/dev/null | tr -d '[:space:]')"
			return 0
		fi
		return 1
	fi
	local diff_out
	diff_out="$(diff "$exp" "$act" 2>/dev/null | head -n 6)" || true
	if [ -n "$diff_out" ]; then
		FIRST_DIFF_REASON="$label diff:
$diff_out"
		return 0
	fi
	return 1
}

compare_outputs() {
	# 0 = 일치, 1 = 미세차. 첫 미세차의 요약을 FIRST_DIFF_REASON에 남긴다.
	FIRST_DIFF_REASON=""
	local out_dir="$1" exp_dir="$2"
	local part
	for part in exit-code stdout stderr tree.txt; do
		if [ ! -f "$exp_dir/$part" ]; then
			FIRST_DIFF_REASON="expected/$part 없음 — capture 를 먼저 실행하세요"
			return 1
		fi
		if first_diff "$part" "$exp_dir/$part" "$out_dir/$part"; then
			return 1
		fi
	done
	return 0
}

# --- 모드 ---------------------------------------------------------------------

fixture_count=0
pass_count=0
fail_count=0
REASON_DIR="$SCRATCH_ROOT/reasons"
mkdir -p "$REASON_DIR"

record_result() {
	local fx="$1" ok="$2" reason="$3"
	fixture_count=$((fixture_count + 1))
	if [ "$ok" = 1 ]; then
		pass_count=$((pass_count + 1))
		printf 'PASS %s\n' "$fx"
	else
		fail_count=$((fail_count + 1))
		printf '%s\n' "$reason" >"$(reason_file "$fx")"
		printf 'FAIL %s\n' "$fx"
		printf '%s\n' "$reason" | sed 's/^/     /'
	fi
}

is_failed() {
	[ -f "$(reason_file "$1")" ]
}

# fixture 키는 bundle/name 슬래시형이라 그대로 파일명에 쓰면 중간 세그먼트가
# 디렉터리로 해석된다. 저장과 조회가 이 한 곳만 보게 평탄화한다.
reason_file() {
	printf '%s/%s.txt' "$REASON_DIR" "$(printf '%s' "$1" | tr '/' '_')"
}

do_capture() {
	pin_check
	local fx fx_dir out_dir fx_list
	# 프로세스 치환(< <(...))은 하위 셸이라 select_fixtures의 fail이 새어 나간다.
	# 메인 셸에서 목록을 먼저 해석해 실패가 스크립트를 끝내게 한다.
	fx_list="$(select_fixtures "$@")"
	while IFS= read -r fx; do
		fx_dir="$SUITE_DIR/$fx"
		out_dir="$fx_dir/expected"
		printf 'capture %s ... ' "$fx"
		LAST_WORKSPACE="$SCRATCH_ROOT/ws"
		run_fixture "$fx_dir" "$out_dir" "$CE_BIN" "$SCRATCH_ROOT/ws"
		printf 'exit=%s\n' "$(cat "$out_dir/exit-code")"
		fixture_count=$((fixture_count + 1))
	done <<<"$fx_list"
	printf 'capture: %d fixture(s) 기록 완료 (CE pin %s)\n' "$fixture_count" "$PARITY_PIN"
}

do_verify() {
	pin_check
	local fx fx_dir out_dir act_dir fx_list
	fx_list="$(select_fixtures "$@")"
	while IFS= read -r fx; do
		fx_dir="$SUITE_DIR/$fx"
		out_dir="$fx_dir/expected"
		act_dir="$SCRATCH_ROOT/actual/$fx"
		mkdir -p "$act_dir"
		LAST_WORKSPACE="$SCRATCH_ROOT/ws"
		run_fixture "$fx_dir" "$act_dir" "$CE_BIN" "$SCRATCH_ROOT/ws"
		if compare_outputs "$act_dir" "$out_dir"; then
			record_result "$fx" 1 ""
		else
			record_result "$fx" 0 "$FIRST_DIFF_REASON"
		fi
	done <<<"$fx_list"

	printf '\nverify: %d/%d 통과 (CE pin %s)\n' "$pass_count" "$fixture_count" "$PARITY_PIN"
	if [ "$fail_count" -gt 0 ]; then
		printf 'verify: 미세차 %d건 — 기준값과 다르게 동작했습니다\n' "$fail_count" >&2
		exit 1
	fi
}

do_report() {
	[ -n "$PRODUCT_BIN" ] || fail "report 모드는 제품 바이너리 경로가 필요합니다: PRODUCT_BIN=... $0 report"
	[ -x "$PRODUCT_BIN" ] || fail "제품 바이너리를 실행할 수 없습니다: $PRODUCT_BIN"
	local report_dir="${PARITY_REPORT_DIR:-$(pwd)/build/parity-report}"
	rm -rf "$report_dir"
	mkdir -p "$report_dir/actual"

	local fx fx_dir out_dir act_dir bin_hash fx_list
	bin_hash="$(hash_stream <"$PRODUCT_BIN")"
	fx_list="$(select_fixtures "$@")"
	while IFS= read -r fx; do
		fx_dir="$SUITE_DIR/$fx"
		out_dir="$fx_dir/expected"
		act_dir="$report_dir/actual/$fx"
		mkdir -p "$act_dir"
		LAST_WORKSPACE="$SCRATCH_ROOT/ws"
		run_fixture "$fx_dir" "$act_dir" "$PRODUCT_BIN" "$SCRATCH_ROOT/ws"
		if compare_outputs "$act_dir" "$out_dir"; then
			record_result "$fx" 1 ""
			rm -rf "$act_dir"
		else
			record_result "$fx" 0 "$FIRST_DIFF_REASON"
		fi
	done <<<"$fx_list"

	# 묶음별 요약과 결과 표를 REPORT.md로 쓴다. 계수는 실행된 fx_list 한정이다 —
	# 선택자로 일부만 실행했을 때 미실행 fixture를 통과로 세지 않도록.
	local bundle
	local bundle_list
	bundle_list="$(printf '%s\n' "$fx_list" | cut -d/ -f1 | LC_ALL=C sort -u)"
	{
		printf '# CE 패리티 보고서 — 제품 바이너리 vs 고정 CE 참조\n\n'
		printf -- '- 참조: CE `%s` (fixture expected는 이 기준으로 캡처됨)\n' "$PARITY_PIN"
		printf -- '- 측정 대상: `%s`\n' "$PRODUCT_BIN"
		printf -- '- 제품 바이너리 sha256: `%s`\n' "$bin_hash"
		printf -- '- 판정: fixture의 expected(stdout/stderr/exit-code/결과 트리)와의 미세차\n\n'
		printf '## 묶음별 요약\n\n'
		printf '| 묶음 | fixture | 통과 | 미세차 |\n|---|---|---|---|\n'
		local b_total b_pass
		while IFS= read -r bundle; do
			b_total=0
			b_pass=0
			while IFS= read -r fx; do
				case "$fx" in
				"$bundle"/*)
					b_total=$((b_total + 1))
					is_failed "$fx" || b_pass=$((b_pass + 1))
					;;
				esac
			done <<<"$fx_list"
			printf '| %s | %d | %d | %d |\n' "$bundle" "$b_total" "$b_pass" "$((b_total - b_pass))"
		done <<<"$bundle_list"
		printf '\n## fixture별 결과\n\n'
		while IFS= read -r fx; do
			if is_failed "$fx"; then
				printf '### FAIL %s\n\n' "$fx"
				printf 'CE 기준 exit-code: %s\n\n' \
					"$(cat "$SUITE_DIR/$fx/expected/exit-code" 2>/dev/null | tr -d '[:space:]')"
				printf '```\n%s\n```\n\n' "$(head -n 8 "$(reason_file "$fx")")"
			else
				printf '### PASS %s\n\n' "$fx"
			fi
		done <<<"$fx_list"
		printf '\n미세차 상세는 %s/actual/<fixture>/ 아래 stdout·stderr·exit-code·tree.txt로 남긴다.\n' "$report_dir"
	} >"$report_dir/REPORT.md"

	printf '\nreport: %d fixture 중 %d 통과, %d 미세차\n' "$fixture_count" "$pass_count" "$fail_count"
	printf 'report: %s\n' "$report_dir/REPORT.md"
}

case "$MODE" in
capture) do_capture "$@" ;;
verify) do_verify "$@" ;;
report) do_report "$@" ;;
esac
