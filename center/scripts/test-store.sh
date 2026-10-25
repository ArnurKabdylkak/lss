#!/usr/bin/env bash
# Тесты хранилища в MongoDB.
#
# Заглушкой драйвер подменять бессмысленно: проверяются как раз
# индексы и поведение базы. Поэтому поднимается настоящая MongoDB —
# временная, на свободном порту, и удаляется после прогона.
#
#   scripts/test-store.sh [дополнительные аргументы go test]
#
# MCHS_TEST_MONGO — прогнать против уже работающей базы. Если в ней
# есть чужие данные, скрипт спросит подтверждение: на машине
# разработчика по адресу 127.0.0.1:27017 обычно висит не та база,
# которая имелась в виду. MCHS_TEST_STORE_FORCE=1 снимает вопрос
# (для CI и для тех, кто точно знает, куда целится).
set -euo pipefail

cd "$(dirname "$0")/.."

pkg=./internal/adapter/outbound/mongostore

# mongo_eval выполняет выражение mongosh на заданном адресе. На хосте
# mongosh может быть не установлен — тогда берём его из образа.
mongo_eval() {
  local uri="$1" script="$2"
  if command -v mongosh > /dev/null 2>&1; then
    mongosh --quiet "$uri" --eval "$script" 2>/dev/null
  else
    docker run --rm --network host mongo:7 \
      mongosh --quiet "$uri" --eval "$script" 2>/dev/null
  fi
}

run_against_given_base() {
  local uri="$1"; shift

  echo "база задана вручную: $uri"

  # Чужие базы на этом сервере — признак того, что адрес указывает не
  # на временную базу, а на чью-то рабочую. Тесты создают и удаляют
  # свою базу, но ошибиться адресом здесь стоит слишком дорого.
  local existing
  existing=$(mongo_eval "$uri" \
    'db.adminCommand({listDatabases:1}).databases
       .map(d => d.name)
       .filter(n => !["admin","config","local"].includes(n))
       .join(", ")' || true)

  if [ -z "$existing" ]; then
    echo "на сервере нет посторонних баз — продолжаю"
  else
    echo
    echo "ВНИМАНИЕ: на этом сервере уже есть базы:"
    echo "  $existing"
    echo
    echo "Тесты создадут свою базу вида mchs_test_* и удалят её после прогона,"
    echo "перечисленных они не касаются. Но убедитесь, что адрес верен."
    echo

    if [ "${MCHS_TEST_STORE_FORCE:-}" = "1" ]; then
      echo "MCHS_TEST_STORE_FORCE=1 — продолжаю без вопроса"
    elif [ -t 0 ]; then
      read -r -p "Продолжить? введите «да»: " answer
      case "$answer" in
        да | Да | ДА) ;;
        *) echo "отменено"; exit 1 ;;
      esac
    else
      echo "Запуск не интерактивный, подтвердить нечем."
      echo "Повторите с MCHS_TEST_STORE_FORCE=1, если адрес верен,"
      echo "или уберите MCHS_TEST_MONGO — тогда поднимется временная база."
      exit 1
    fi
  fi

  MCHS_TEST_MONGO="$uri" go test "$pkg" "$@"
}

if [ -n "${MCHS_TEST_MONGO:-}" ]; then
  run_against_given_base "$MCHS_TEST_MONGO" "$@"
  exit $?
fi

name="mchs-test-mongo-$$"
# Порт выбирает Docker: фиксированный занят ровно тогда, когда рядом
# уже идёт другой прогон.
docker run -d --rm --name "$name" -p 127.0.0.1::27017 mongo:7 \
  --quiet --bind_ip_all > /dev/null

cleanup() { docker rm -f "$name" > /dev/null 2>&1 || true; }
trap cleanup EXIT

port=$(docker port "$name" 27017/tcp | head -1 | sed 's/.*://')
uri="mongodb://127.0.0.1:${port}"
echo "поднята временная MongoDB: $uri"

# Ждём готовности: база поднимается пару секунд, и тест не должен
# падать из-за того, что пришёл первым.
for _ in $(seq 1 30); do
  if docker exec "$name" mongosh --quiet --eval 'db.runCommand({ping:1}).ok' > /dev/null 2>&1; then
    break
  fi
  sleep 1
done

MCHS_TEST_MONGO="$uri" go test "$pkg" "$@"
