#!/usr/bin/env bash
# Runs the acceptance scenarios (internal/api/scenarios_*_test.go) against a
# throwaway MongoDB, then prints the French report: one line per sentence, OK or
# KO, validated or only observed.
#
#   make scenarios                      every scenario
#   backend/scripts/scenarios.sh -run 'TestScenarios_Rules'   a subset
#
# Where the MongoDB comes from, first match wins:
#   1. MAILSORTER_TEST_MONGO_URI, if you already run one (CI sets it)
#   2. Docker, if a daemon answers: a mongo:7 container, removed afterwards
#   3. otherwise the official mongod 7.0 binary for this OS, downloaded once
#      into ~/.cache/mailsorter-mongo, started on a temp directory, killed
#      afterwards. This is what makes the scenarios run in a cloud session
#      without Docker.
#
# The report path is SCENARIO_REPORT, or a temp file. Exit code is go test's.
set -euo pipefail

cd "$(dirname "$0")/.."

MONGO_VERSION=7.0.43
report="${SCENARIO_REPORT:-$(mktemp "${TMPDIR:-/tmp}/scenarios.XXXXXX")}"
cleanup=()
on_exit() {
  for c in "${cleanup[@]+"${cleanup[@]}"}"; do eval "$c" || true; done
}
trap on_exit EXIT

port_open() { (exec 3<>"/dev/tcp/127.0.0.1/$1") 2>/dev/null; }

wait_for() {
  for _ in $(seq 1 60); do
    if port_open "$1"; then return 0; fi
    sleep 0.5
  done
  echo "MongoDB n'a pas démarré sur le port $1" >&2
  return 1
}

free_port() {
  local p=27027
  while port_open "$p"; do p=$((p + 1)); done
  echo "$p"
}

if [ -n "${MAILSORTER_TEST_MONGO_URI:-}" ]; then
  uri="$MAILSORTER_TEST_MONGO_URI"
elif command -v docker >/dev/null 2>&1 && docker info >/dev/null 2>&1; then
  port="$(free_port)"
  name="mailsorter-scenarios-$$"
  docker run -d --rm --name "$name" -p "127.0.0.1:$port:27017" mongo:7 >/dev/null
  cleanup+=("docker rm -f $name >/dev/null 2>&1")
  wait_for "$port"
  uri="mongodb://127.0.0.1:$port"
else
  case "$(uname -s)-$(uname -m)" in
    Linux-x86_64)  dist="linux/mongodb-linux-x86_64-ubuntu2204-$MONGO_VERSION" ;;
    Linux-aarch64) dist="linux/mongodb-linux-aarch64-ubuntu2204-$MONGO_VERSION" ;;
    Darwin-arm64)  dist="osx/mongodb-macos-arm64-$MONGO_VERSION" ;;
    Darwin-x86_64) dist="osx/mongodb-macos-x86_64-$MONGO_VERSION" ;;
    *)
      echo "Pas de Docker et pas de binaire MongoDB connu pour $(uname -s)-$(uname -m)." >&2
      echo "Lance une MongoDB 7 et passe son adresse dans MAILSORTER_TEST_MONGO_URI." >&2
      exit 1
      ;;
  esac
  cache="$HOME/.cache/mailsorter-mongo"
  mongod="$cache/$(basename "$dist")/bin/mongod"
  if [ ! -x "$mongod" ]; then
    echo "Téléchargement de MongoDB $MONGO_VERSION (une seule fois)..." >&2
    mkdir -p "$cache"
    curl -fsSL "https://fastdl.mongodb.org/$dist.tgz" | tar xz -C "$cache"
  fi
  port="$(free_port)"
  dbpath="$(mktemp -d "${TMPDIR:-/tmp}/scenarios-mongo.XXXXXX")"
  "$mongod" --dbpath "$dbpath" --port "$port" --bind_ip 127.0.0.1 --quiet \
    --logpath "$dbpath/mongod.log" >/dev/null 2>&1 &
  pid=$!
  cleanup+=("kill $pid 2>/dev/null; wait $pid 2>/dev/null; rm -rf '$dbpath'")
  wait_for "$port"
  uri="mongodb://127.0.0.1:$port"
fi

set +e
MAILSORTER_TEST_MONGO_URI="$uri" SCENARIO_REPORT="$report" \
  go test -tags acceptance -count=1 -run '^TestScenarios' "$@" ./internal/api/
code=$?
set -e

if [ -s "$report" ]; then
  echo
  cat "$report"
fi
exit "$code"
