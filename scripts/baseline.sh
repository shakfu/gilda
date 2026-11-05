#!/usr/bin/env bash
# Runs the eval baseline: every task in evals/, N runs each, for each model, with the working
# rules on and off ([prompt] rules), so one run records the baseline and the rules' A/B.
#
#   scripts/baseline.sh                         # default models whose keys are set
#   MODELS="openai:gpt-5.5" N=3 scripts/baseline.sh
#
# Runs use --permissions all; run it in a container or a disposable checkout. Each arm of each
# model is 13 tasks x N runs of real API calls; the script prints the count and waits 5 s.
set -euo pipefail
cd "$(dirname "$0")/.."

N=${N:-3}
TIMEOUT=${TIMEOUT:-10m}
if [[ -z ${MODELS:-} ]]; then
	MODELS=""
	[[ -n ${ANTHROPIC_API_KEY:-}${ANTHROPIC_AUTH_TOKEN:-} ]] && MODELS+=" anthropic:claude-opus-5"
	[[ -n ${OPENAI_API_KEY:-} ]] && MODELS+=" openai:gpt-5.5"
	[[ -z $MODELS && -n ${OPENROUTER_API_KEY:-} ]] && MODELS+=" openrouter:anthropic/claude-opus-5"
fi
if [[ -z ${MODELS// /} ]]; then
	echo "baseline: no MODELS given and no provider key set" >&2
	exit 2
fi

tasks=(evals/*/)
out=evals/results/baseline-$(date +%Y%m%d-%H%M%S)
mkdir -p "$out"
printf '[prompt]\nrules = false\n' >"$out/off.toml"
printf '[prompt]\nrules = true\n' >"$out/on.toml"

arms=$(($(wc -w <<<"$MODELS") * 2))
echo "baseline: ${#tasks[@]} tasks x $N runs x $arms arms = $((${#tasks[@]} * N * arms)) runs into $out" >&2
sleep 5

make -s build
for m in $MODELS; do
	for arm in on off; do
		dir="$out/${m//[:\/]/_}-rules-$arm"
		echo "== $m, rules $arm" | tee -a "$out/report.txt"
		go run ./scripts/eval -m "$m" -n "$N" -timeout "$TIMEOUT" -settings "$out/$arm.toml" -out "$dir" "${tasks[@]}" |
			tee -a "$out/report.txt"
		echo | tee -a "$out/report.txt"
	done
done
echo "baseline: report in $out/report.txt; record it in docs/dev/eval-baseline.md" >&2
