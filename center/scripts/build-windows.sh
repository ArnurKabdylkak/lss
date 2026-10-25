#!/usr/bin/env bash
# Сборка приложения для Windows: один файл, внутри всё нужное.
#
# Внутрь кладутся панель, ffmpeg и установщик компонента просмотра
# веб-страниц. Оператор на объекте ничего не скачивает и не ставит:
# сети наружу там нет, прав администратора может не быть, а
# инструкция «доложите такой-то файл рядом» живёт ровно до первого
# человека, который её не прочитал.
#
#   scripts/build-windows.sh
#   OUT=/tmp/mchs scripts/build-windows.sh
set -euo pipefail

ROOT=$(cd "$(dirname "$0")/.." && pwd)
PANEL=${PANEL:-$ROOT/../panel}
OUT=${OUT:-$ROOT/dist/windows}

# Куда go:embed смотрит за вложениями. В репозитории их нет: держать
# там чужие бинари на сотню мегабайт незачем, а сборка берёт
# проверенные версии по известным адресам.
EMBED_PANEL=$ROOT/internal/adapter/inbound/httpapi/paneldist
BUNDLE=$ROOT/internal/adapter/outbound/deps/bundle

# Версия ffmpeg фиксированная: «последняя» однажды окажется той, в
# которой поменяли ключ командной строки, и видео встанет на объекте.
FFMPEG_URL=${FFMPEG_URL:-https://github.com/GyanD/codexffmpeg/releases/download/7.1/ffmpeg-7.1-essentials_build.zip}
WEBVIEW_URL=${WEBVIEW_URL:-https://go.microsoft.com/fwlink/p/?LinkId=2124703}

say() { printf '  %s\n' "$*"; }
step() { printf '\n== %s\n' "$*"; }

step "Панель"
[[ -d $PANEL ]] || { echo "нет каталога панели: $PANEL" >&2; exit 1; }
( cd "$PANEL" && npm run build >/dev/null )
rm -rf "$EMBED_PANEL"
mkdir -p "$EMBED_PANEL"
cp -r "$PANEL/dist/." "$EMBED_PANEL/"
say "собрана"

step "Вложения"
mkdir -p "$BUNDLE"

if [[ ! -f $BUNDLE/ffmpeg.exe.gz ]]; then
    say "качаю ffmpeg…"
    tmp=$(mktemp -d)
    curl -sL -o "$tmp/ff.zip" "$FFMPEG_URL"
    unzip -q -o "$tmp/ff.zip" -d "$tmp/ff"
    gzip -9 -c "$(find "$tmp/ff" -name ffmpeg.exe | head -1)" > "$BUNDLE/ffmpeg.exe.gz"
    rm -rf "$tmp"
fi
say "ffmpeg: $(du -h "$BUNDLE/ffmpeg.exe.gz" | cut -f1)"

if [[ ! -f $BUNDLE/wv2setup.exe.gz ]]; then
    say "качаю установщик WebView2…"
    tmp=$(mktemp -d)
    curl -sL --max-redirs 10 -o "$tmp/wv2.exe" "$WEBVIEW_URL"
    gzip -9 -c "$tmp/wv2.exe" > "$BUNDLE/wv2setup.exe.gz"
    rm -rf "$tmp"
fi
say "WebView2: $(du -h "$BUNDLE/wv2setup.exe.gz" | cut -f1)"

step "Сборка"
mkdir -p "$OUT"

# Приложение: окно с панелью, центр внутри, вложения внутри.
# -H windowsgui убирает чёрное окно консоли.
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 \
    go build -tags "embedpanel withdeps" -trimpath \
    -ldflags="-s -w -H windowsgui" \
    -o "$OUT/Тренажёрный комплекс.exe" ./cmd/mchs-desktop
say "$(du -h "$OUT/Тренажёрный комплекс.exe" | cut -f1)  Тренажёрный комплекс.exe"

# Служебный вход: тот же центр без окна — для машины, которая работает
# без оператора за экраном.
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 \
    go build -tags embedpanel -trimpath -ldflags="-s -w" \
    -o "$OUT/mchs-center.exe" ./cmd/telemetryd
say "$(du -h "$OUT/mchs-center.exe" | cut -f1)  mchs-center.exe"

GOOS=windows GOARCH=amd64 CGO_ENABLED=0 \
    go build -trimpath -ldflags="-s -w" \
    -o "$OUT/mchs-users.exe" ./cmd/mchs-users
say "$(du -h "$OUT/mchs-users.exe" | cut -f1)  mchs-users.exe"

step "Настройки объекта"
for f in rooms.yaml users.json; do
    if [[ -f $ROOT/$f && ! -f $OUT/$f ]]; then
        cp "$ROOT/$f" "$OUT/$f"
        say "$f скопирован"
    else
        say "$f оставлен как есть"
    fi
done
mkdir -p "$OUT/sounds"
cp -n "$ROOT"/sounds/*.mp3 "$ROOT"/sounds/*.wav "$OUT/sounds/" 2>/dev/null || true
cp -n "$ROOT/sounds/README.md" "$OUT/sounds/" 2>/dev/null || true

# Панель уже внутри: в дереве исходников её копия не нужна.
rm -rf "$EMBED_PANEL"

step "Готово"
say "Комплект: $OUT"
say "Запуск — двойным щелчком по «Тренажёрный комплекс.exe»."
