# Сборка статического бинаря: внешние зависимости чистые на Go,
# поэтому CGO не нужен и итоговый образ может быть пустым.
FROM golang:1.25 AS build

# Общий модуль телеметрии лежит рядом с этим репозиторием и подключён
# директивой replace. Каталог сборки его не содержит, поэтому он
# приходит отдельным именованным контекстом:
#
#	docker build --build-context shared=../telemetry .
#
# Так сохраняется и один источник правды, и маленький контекст: брать
# контекстом общий родительский каталог значило бы тащить в демон всё,
# что лежит рядом.
COPY --from=shared . /src/telemetry

WORKDIR /src/center

# Зависимости отдельным слоем: кеш сборки не поедет от правки кода.
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download

COPY . .

# Тесты в образ не встроены: они гоняются перед выкладкой (make test),
# а здесь удваивали бы время каждого подъёма центра.

# -trimpath убирает пути сборочной машины из бинаря,
# -w -s выбрасывают отладочную информацию.
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build CGO_ENABLED=0 go build -trimpath -ldflags="-w -s" -o /out/telemetryd ./cmd/telemetryd

# Утилита заведения учётных записей едет в образ с видео: на хосте,
# где стоит только Docker (например, на Windows), завести оператора
# больше нечем.
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build CGO_ENABLED=0 go build -trimpath -ldflags="-w -s" -o /out/mchs-users ./cmd/mchs-users

# Каталог для тома готовится здесь: в scratch нет mkdir и chown, а том
# наследует владельца от каталога в образе. Без этого шага процесс под
# непривилегированным пользователем не создаст CSV.
RUN mkdir -p /out/data

# Сборка панели. Исходники лежат в соседнем репозитории и приходят
# именованным контекстом, как и общий модуль:
#
#	docker build --build-context panel=../panel …
#
# Так на хосте не нужен Node: панель собирается внутри сборки и дальше
# едет статикой, которую раздаёт сам центр.
FROM node:22-alpine AS panel

WORKDIR /app

COPY --from=panel-src package.json package-lock.json ./
RUN npm ci --no-audit --no-fund

COPY --from=panel-src . .
RUN npm run build

# Образ с видеонаблюдением: трансляции ведёт ffmpeg, поэтому пустой
# образ тут не годится. Собирается явно:
#
#	docker build --target video -t mchs/telemetryd:video .
#
# Без камер эта стадия не нужна — цель по умолчанию осталась пустой.
FROM alpine:3.22 AS video

# Пользователь 65534 (nobody) в alpine уже есть — его и используем.
RUN apk add --no-cache ffmpeg tzdata \
 && mkdir -p /data /video /records \
 && chown 65534:65534 /data /video /records

COPY --from=build /out/telemetryd /telemetryd
COPY --from=build /out/mchs-users /mchs-users
COPY --from=panel /app/dist /panel

USER 65534:65534
VOLUME ["/data"]
EXPOSE 5005/udp 8080/tcp

ENV MCHS_LISTEN=:5005 \
    MCHS_HTTP_ADDR=:8080 \
    MCHS_CSV=/data/telemetry.csv \
    MCHS_ROOMS=/etc/mchs/rooms.yaml \
    MCHS_VIDEO_ROOT=/video \
    MCHS_RECORDS=/records \
    MCHS_PANEL=/panel \
    TZ=Europe/Moscow

ENTRYPOINT ["/telemetryd"]

# Пустой образ: нет shell, пакетного менеджера и лишних файлов —
# нечего эксплуатировать и нечего обновлять.
FROM scratch

# Часовые пояса: без них контейнер живёт в UTC. Временные метки в CSV
# и в API пишутся в локальном времени процесса, поэтому зону задаёт TZ.
COPY --from=build /usr/share/zoneinfo /usr/share/zoneinfo

COPY --from=build /out/telemetryd /telemetryd

# Непривилегированный пользователь. В scratch нет /etc/passwd,
# поэтому задаётся числовым идентификатором.
USER 65534:65534

# Каталог для CSV. Монтируется томом: иначе поток телеметрии
# останется внутри контейнера и пропадёт с ним.
COPY --from=build --chown=65534:65534 /out/data /data
VOLUME ["/data"]

EXPOSE 5005/udp 8080/tcp

ENV MCHS_LISTEN=:5005 \
    MCHS_HTTP_ADDR=:8080 \
    MCHS_CSV=/data/telemetry.csv \
    TZ=Europe/Moscow

ENTRYPOINT ["/telemetryd"]
