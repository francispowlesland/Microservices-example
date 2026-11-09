#!/bin/sh
# Generates JWT keys into .env (if missing), then starts the stack.
# Pass FORCE_KEYS=1 to regenerate; that invalidates all issued tokens.
set -eu
cd "$(dirname "$0")"

if [ ! -f .env ] || [ "${FORCE_KEYS:-}" = 1 ]; then
  keys="$(go run ./tools/keygen)"
  {
    printf 'JWT_PRIVATE_KEY="%s"\n' "$(printf '%s\n' "$keys" | sed -n '/BEGIN PRIVATE KEY/,/END PRIVATE KEY/p')"
    printf 'JWT_PUBLIC_KEY="%s"\n'  "$(printf '%s\n' "$keys" | sed -n '/BEGIN PUBLIC KEY/,/END PUBLIC KEY/p')"
  } > .env
  chmod 600 .env
  echo "Generated keys in .env"
fi

exec docker compose up --build "$@"
