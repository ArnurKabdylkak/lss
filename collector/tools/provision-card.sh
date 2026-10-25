#!/usr/bin/env bash
# Подготовка карты сборщика напрямую, без ansible.
#
# Нужно, когда узла ещё нет в сети: ansible требует SSH, а SSH требует
# настроенной карты. Скрипт закрывает именно этот разрыв — ставит доступ
# и сам сборщик на смонтированную файловую систему. Дальнейшие
# обновления идут штатно, плейбуком.
#
#   sudo tools/provision-card.sh 1                    # теплокамера, .10
#   sudo tools/provision-card.sh 2                    # дымокамера-1, .11
#   sudo tools/provision-card.sh 3                    # дымокамера-2, .12
#   sudo tools/provision-card.sh 3 192.168.1.42/24    # другой адрес
#   sudo EXTRA_IP=192.168.0.4/24 tools/provision-card.sh 3  # и в текущей сети
#
# Третий аргумент — открытый ключ, если он лежит не в ~/.ssh/id_rsa.pub.
#
# Запись идёт в /etc и /opt карты, поэтому нужен root. Повторный запуск
# безопасен: все правки идемпотентны.
set -euo pipefail

# --- что и куда -------------------------------------------------------

ROOT=${ROOT:-/run/media/$SUDO_USER/rootfs}
BOOT=${BOOT:-/run/media/$SUDO_USER/bootfs}

# Комплекс и комната. Адреса по network-plan.xlsx: сеть плоская, одна
# подсеть 192.168.1.0/24, порядок держится диапазонами последнего октета
# (.10-.19 сборщики, .50-.79 платы).
#
# По умолчанию — RPi-2, первая комната дымокамеры (.11).
#   RPi-1 теплокамера:   ROOM=heat   IP=192.168.1.10/24
#   RPi-3 дымокамера-2:  ROOM=smoke2 IP=192.168.1.12/24
#
# site_id у всех трёх сборщиков один: комплекс в центре один, а комнаты —
# его части. Поэтому различать устройства приходится именем: device_id
# обязан быть уникален на весь комплекс — по нему центр раскладывает их
# по комнатам и закрепляет показания за карточкой спасателя. Отсюда
# приставка комнаты у каждого имени.
# Комнаты зовутся по номеру: так их знают и сеть объекта, и инвентарь
# ansible, и центр (obj-17-rpi-N).
NUM=${1:-${NUM:-3}}
NUM=${NUM#room}

case $NUM in
    1|2|3) ;;
    *) echo "комната задаётся номером 1, 2 или 3 (получено: $NUM)" >&2; exit 1 ;;
esac

# Объект обязан совпадать с полем site в rooms.yaml центра: очередь
# команд ведётся по нему, и расхождение означало бы, что сборщик
# опрашивает пустую очередь.
SITE=${SITE:-obj-17}
ROOM=room$NUM
COLLECTOR=${COLLECTOR:-$SITE-rpi-$NUM}

# Адреса по network-plan.xlsx: сборщики .10-.12.
declare -A ROOM_IP=([1]=192.168.1.10/24 [2]=192.168.1.11/24 [3]=192.168.1.12/24)
IP=${2:-${IP:-${ROOM_IP[$NUM]}}}
GW=${GW:-192.168.1.1}

# Имя хоста. Комплекс у трёх сборщиков общий, поэтому в имя идёт
# комната — иначе в сети окажутся три узла с одинаковым именем.
HOST=${HOST:-rpi_$ROOM}

# Дополнительный адрес на том же интерфейсе, если узел нужно застать и в
# другой сети. Пусто — только целевой.
EXTRA_IP=${EXTRA_IP:-}

CENTER=${CENTER:-http://192.168.1.2:8080}   # узел центра
NTP=${NTP:-192.168.1.3}
DNS=${DNS:-$GW}

# Пользователь для входа: свой на каждой комнате, как заведено на
# объекте. Если его нет в образе — заводится здесь же, с запертым
# паролем: вход по ключу, а пароль для консоли ставится на месте
# командой passwd.
LOGIN=${LOGIN:-$ROOM}
PUBKEY=${3:-${PUBKEY:-/home/$SUDO_USER/.ssh/id_rsa.pub}}

BINARY=${BINARY:-bin/collector-arm64}
SERVICE_USER=mchs-svc

say() { printf '  %s\n' "$*"; }
step() { printf '\n== %s\n' "$*"; }

# --- проверки ---------------------------------------------------------

[[ $EUID -eq 0 ]] || { echo "нужен root: sudo $0" >&2; exit 1; }
[[ -n ${SUDO_USER:-} ]] || { echo "запускать через sudo, не из-под root напрямую" >&2; exit 1; }

for p in "$ROOT/etc/os-release" "$BOOT/config.txt"; do
    [[ -e $p ]] || { echo "не похоже на карту Raspberry Pi: нет $p" >&2; exit 1; }
done
if [[ ! -f $BINARY ]]; then
    echo "  бинаря $BINARY нет, собираю под aarch64…"
    ( cd "$(dirname "$0")/.." && sudo -u "$SUDO_USER" env CGO_ENABLED=0 GOOS=linux GOARCH=arm64 \
        go build -trimpath -ldflags="-s -w" -o "$BINARY" ./cmd ) \
        || { echo "сборка не удалась — соберите вручную: make build-arm64" >&2; exit 1; }
fi
[[ -f $PUBKEY ]] || { echo "нет открытого ключа $PUBKEY" >&2; exit 1; }

# Карту легко перепутать с собственным корнем, а правки тут делаются от
# root. Отдельная проверка: на работающей системе есть /proc/1.
[[ ! -e $ROOT/proc/1 ]] || { echo "$ROOT выглядит как живая система, а не карта" >&2; exit 1; }

# Раскладка комнаты. Логика у трёх сборщиков одна, различаются адреса
# плат и состав обвязки — поэтому это таблица, а не три ветки в коде.
case $NUM in
1)
    DEVICES='  - addr: "192.168.1.50"
    id: "'$SITE'-load-1"
    limits:
      period_ms: 200

  # - addr: "192.168.1.51"
  #   id: "'$SITE'-bike-1"
  # - addr: "192.168.1.52"
  #   id: "'$SITE'-stairs-1"
'
    SENSOR_ID="$COLLECTOR-temp"
    UNITS='    - {unit: light, outputs: [gpio-17], on_prepare: true,  on_alarm: "on"}
    - {unit: fan,   outputs: [gpio-27], on_prepare: false, on_alarm: "on"}
    - {unit: heat,  outputs: [gpio-22], on_prepare: true,  on_alarm: "off"}
'
    RUBBLE=""
    ;;
2)
    DEVICES='  - addr: "192.168.1.60"
    id: "'$SITE'-code-1"
    limits:
      period_ms: 200

  # - addr: "192.168.1.61"
  #   id: "'$SITE'-fire-1"
  # - addr: "192.168.1.62"
  #   id: "'$SITE'-maze-1"
'
    SENSOR_ID="$SITE-smoke-temp-2"
    UNITS='    - {unit: smoke,  outputs: [gpio-17], on_prepare: true,  on_alarm: "off"}
    - {unit: flood,  outputs: [gpio-27], on_prepare: true,  on_alarm: "off"}
    - {unit: strobe, outputs: [gpio-22], on_prepare: true,  on_alarm: "off"}
    - {unit: sound,  outputs: [gpio-6],  on_prepare: true,  on_alarm: "off"}
    - {unit: light,  outputs: [gpio-13], on_prepare: false, on_alarm: "on"}
    - {unit: fan,    outputs: [gpio-19], on_prepare: false, on_alarm: "on"}
'
    RUBBLE=""
    ;;
3)
    DEVICES='  - addr: "192.168.1.70"
    id: "'$SITE'-fire-2"
    limits:
      period_ms: 200

  # - addr: "192.168.1.71"
  #   id: "'$SITE'-rubble-1"
  # - addr: "192.168.1.72"
  #   id: "'$SITE'-door-1"
'
    SENSOR_ID="$SITE-smoke-temp-3"
    UNITS='    - {unit: smoke,  outputs: [gpio-17], on_prepare: true,  on_alarm: "off"}
    - {unit: flood,  outputs: [gpio-27], on_prepare: true,  on_alarm: "off"}
    - {unit: strobe, outputs: [gpio-22], on_prepare: true,  on_alarm: "off"}
    - {unit: sound,  outputs: [gpio-6],  on_prepare: true,  on_alarm: "off"}
    - {unit: light,  outputs: [gpio-13], on_prepare: false, on_alarm: "on"}
    - {unit: fan,    outputs: [gpio-19], on_prepare: false, on_alarm: "on"}
'
    # Магниты «Завалов» питаются отдельной цепью от дверных замков:
    # аварийное открытие дверей не должно ронять трубы, и по аварии
    # этот вывод не трогается.
    RUBBLE='  rubble: [gpio-26]
'
    ;;
esac

say "карта:    $ROOT (+ $BOOT)"
say "комплекс: $SITE, комната $NUM (узел $HOST, сборщик $COLLECTOR)"
say "адрес:    $IP, шлюз $GW${EXTRA_IP:+ (ещё $EXTRA_IP)}"
say "центр:    $CENTER"

# --- доступ по SSH ----------------------------------------------------

step "SSH"

# В образе служба есть, но не включена: Raspberry Pi OS ждёт файла
# /boot/ssh либо firstrun. Включаем явно — так состояние видно в
# файловой системе, а не зависит от разового маркера.
install -d -m 755 "$ROOT/etc/systemd/system/multi-user.target.wants"
ln -sfn /lib/systemd/system/ssh.service \
    "$ROOT/etc/systemd/system/multi-user.target.wants/ssh.service"
say "ssh.service включён"

# Вход только по ключу: пароля у pi в чистом образе нет вовсе, и
# заводить его незачем.
home=$(awk -F: -v u="$LOGIN" '$1==u {print $6}' "$ROOT/etc/passwd")
if [[ -z $home ]]; then
    # В чистом образе есть только pi. Заводим учётную запись комнаты
    # прямо в файлах карты: подобрать свободный uid здесь можно —
    # образ пустой, и кроме pi (1000) в нём никого нет.
    uid=1001
    while awk -F: -v i="$uid" '$3==i {found=1} END {exit !found}' "$ROOT/etc/passwd"; do
        uid=$((uid + 1))
    done
    home=/home/$LOGIN

    echo "$LOGIN:x:$uid:$uid:Сборщик комнаты $NUM:$home:/bin/bash" >> "$ROOT/etc/passwd"
    # Пароль заперт (!): вход только по ключу. Пароль для консоли
    # задаётся на месте и в репозиторий не попадает.
    echo "$LOGIN:!:20000:0:99999:7:::" >> "$ROOT/etc/shadow"
    echo "$LOGIN:x:$uid:" >> "$ROOT/etc/group"

    # Группы доступа к железу: без gpio и i2c сборщик не достучится до
    # реле и датчика, остальные — как у штатного pi.
    for g in adm dialout sudo audio video plugdev users input render netdev spi i2c gpio; do
        grep -q "^$g:" "$ROOT/etc/group" || continue
        sed -i "s/^\($g:[^:]*:[^:]*:\)\(.*\)$/\1\2,$LOGIN/; s/^\($g:[^:]*:[^:]*:\),/\1/" \
            "$ROOT/etc/group"
    done
    install -d -m 755 -o "$uid" -g "$uid" "$ROOT$home"
    say "заведена учётная запись $LOGIN (uid $uid), пароль заперт"
fi
uid=$(awk -F: -v u="$LOGIN" '$1==u {print $3}' "$ROOT/etc/passwd")
gid=$(awk -F: -v u="$LOGIN" '$1==u {print $4}' "$ROOT/etc/passwd")

install -d -m 700 -o "$uid" -g "$gid" "$ROOT$home/.ssh"
install -m 600 -o "$uid" -g "$gid" "$PUBKEY" "$ROOT$home/.ssh/authorized_keys"
say "ключ положен в $home/.ssh/authorized_keys"

# Оболочка: в чистом образе у pi стоит nologin до прохождения firstboot.
sed -i "s|^$LOGIN:\(.*\):/usr/sbin/nologin$|$LOGIN:\1:/bin/bash|" "$ROOT/etc/passwd"
say "оболочка $LOGIN: $(awk -F: -v u="$LOGIN" '$1==u {print $7}' "$ROOT/etc/passwd")"

# sudo без пароля. Пароля у учётной записи нет, поэтому обычный sudo
# просто не сработает — ни руками, ни из плейбука. Вход при этом
# возможен только по ключу.
cat > "$ROOT/etc/sudoers.d/010-mchs" <<EOF
# Создано tools/provision-card.sh
$LOGIN ALL=(ALL) NOPASSWD: ALL
EOF
chmod 440 "$ROOT/etc/sudoers.d/010-mchs"
say "sudo без пароля для $LOGIN"

# --- статический адрес ------------------------------------------------

step "Сеть"

# NetworkManager, а не dhcpcd: в Debian 13 на Raspberry Pi OS сетью
# управляет он, и dhcpcd.conf в образе остаётся не задействован.
#
# Адрес статический: плата опознаётся сборщиком по адресу, а центр
# ожидает сборщик на известном месте — динамический адрес сломал бы и
# карту плат, и правила межсетевого экрана.
conn="$ROOT/etc/NetworkManager/system-connections/mchs-ethernet.nmconnection"
install -d -m 755 "$ROOT/etc/NetworkManager/system-connections"

# Чужие профили на том же интерфейсе убираем: два профиля на eth0 —
# верный способ получить непредсказуемый адрес после перезагрузки.
# Копии остаются на карте, в /root/net-backup.
for old in "$ROOT"/etc/NetworkManager/system-connections/*.nmconnection; do
    [[ -e $old ]] || continue
    [[ $(basename "$old") == mchs-ethernet.nmconnection ]] && continue
    grep -q "interface-name=eth0" "$old" || continue
    install -d -m 700 "$ROOT/root/net-backup"
    mv "$old" "$ROOT/root/net-backup/"
    say "прежний профиль $(basename "$old") убран в /root/net-backup"
done
cat > "$conn" <<EOF
# Создано tools/provision-card.sh
[connection]
id=mchs-ethernet
type=ethernet
interface-name=eth0
autoconnect=true
autoconnect-priority=100

[ipv4]
method=manual
address1=$IP,$GW
${EXTRA_IP:+address2=$EXTRA_IP}
dns=$DNS;
may-fail=false

[ipv6]
method=disabled
EOF
# NetworkManager отвергает профиль с правами шире 600.
chmod 600 "$conn"
say "профиль mchs-ethernet на eth0: $IP, шлюз $GW${EXTRA_IP:+, плюс $EXTRA_IP}"

# Имя хоста. Без записи в hosts каждый sudo ждёт таймаут разрешения
# имени: команды тормозят, а плейбук падает на поднятии прав.
host="$HOST"
echo "$host" > "$ROOT/etc/hostname"
# Имя у машины одно: прежние строки 127.0.1.1 (от образа или от
# предыдущей подготовки) убираем, иначе sudo и ssh разрешают имя во
# что придётся.
sed -i '/^127\.0\.1\.1[[:space:]]/d' "$ROOT/etc/hosts"
printf '127.0.1.1\t%s\n' "$host" >> "$ROOT/etc/hosts"
say "имя хоста: $host"

# --- время ------------------------------------------------------------

step "Время"

# Метку приёма ставит сам сборщик, а у Raspberry Pi нет часов с
# батарейкой: после обесточивания шкала уезжает, и показания не лягут на
# историю в центре. Сервер времени стоит внутри контура — наружу выхода
# нет ни у одного сегмента.
install -d -m 755 "$ROOT/etc/systemd/timesyncd.conf.d"
cat > "$ROOT/etc/systemd/timesyncd.conf.d/mchs.conf" <<EOF
# Создано tools/provision-card.sh
[Time]
NTP=$NTP
EOF
chmod 644 "$ROOT/etc/systemd/timesyncd.conf.d/mchs.conf"
say "сервер времени: $NTP"

# --- сборщик ----------------------------------------------------------

step "Сборщик"

install -d -m 755 "$ROOT/opt/mchs/bin"
install -m 755 "$BINARY" "$ROOT/opt/mchs/bin/collector"
say "бинарь: /opt/mchs/bin/collector ($(du -h "$BINARY" | cut -f1))"

# Пользователь службы создаётся при первой загрузке: подбирать свободный
# uid вручную в чужой файловой системе — верный способ получить
# конфликт. Строка m добавляет его в группы доступа к железу.
install -d -m 755 "$ROOT/etc/sysusers.d"
cat > "$ROOT/etc/sysusers.d/mchs.conf" <<EOF
# Создано tools/provision-card.sh
u $SERVICE_USER - "Сборщик телеметрии МЧС" /nonexistent /usr/sbin/nologin
m $SERVICE_USER gpio
m $SERVICE_USER i2c
EOF
chmod 644 "$ROOT/etc/sysusers.d/mchs.conf"
say "пользователь службы $SERVICE_USER: создастся при первой загрузке"

install -d -m 750 "$ROOT/etc/mchs"
cat > "$ROOT/etc/mchs/config.yml" <<EOF
# Создано tools/provision-card.sh. Дальнейшие правки — плейбуком.
site: $SITE

listen: ":5005"
http: ":8080"

silence: 3s

center:
  # telemetryd БЕЗ видео. У центра это два отдельных процесса именно
  # потому, что от телеметрии зависит тепловая защита, и ронять её
  # вместе с видеотрактом нельзя (ARCHITECTURE.md §6.2).
  #
  # Токен не нужен: вход сборщика в центре открыт без сессии.
  url: "$CENTER"
  token: ""
  every: 1s
  timeout: 10s
  max_batch: 500

buffer:
  path: "/var/lib/mchs/buffer.jsonl"
  max_records: 200000

# Карта плат комнаты. Плата своего идентификатора не передаёт, поэтому
# опознаётся по адресу; адрес зашит в прошивке, менять его без правки
# этой карты нельзя.
#
# Диапазон комнаты по network-plan.xlsx: первая комната дымокамеры —
# .60-.62. Пакет с адреса вне карты отбрасывается (accept_unknown:
# false), и в журнале видно, с какого именно.
#
# Идентификаторы согласовать с rooms.yaml центра, иначе устройство не
# попадёт в комнату.
devices:
$DEVICES
accept_unknown: false

sensors:
  enabled: true
  device: "/dev/i2c-1"
  # BMP280. Адрес зависит от платы: 0x76 или 0x77 — сверить
  # i2cdetect -y 1. Нет датчика — сборщик продолжит без него.
  addr: 0x77
  every: 1s
  # Имя согласовано с rooms.yaml центра: у дымокамер это датчик
  # воздуха комнаты (поле sensor), у теплокамеры — свой датчик
  # сборщика рядом с пушкой.
  id: "$SENSOR_ID"

# Реле на выводах сборщика.
outputs:
  enabled: true
  chip: "/dev/gpiochip0"
  active_low: true
  open_drain: true

  lines:
    - id: gpio-17
      line: 17
      kind: switch

    - id: gpio-27
      line: 27
      kind: switch

    - id: gpio-22
      line: 22
      kind: switch

    - id: gpio-5
      line: 5
      kind: switch

    - id: gpio-6
      line: 6
      kind: switch

    - id: gpio-13
      line: 13
      kind: switch

    - id: gpio-19
      line: 19
      kind: switch

    - id: gpio-26
      line: 26
      kind: switch

# Оборудование помещения. Команды подготовки, аварии и поштучного
# управления центр адресует сборщику (room.id), а какое реле за что
# отвечает, знает он: в центре ни выводов, ни полярности нет.
room:
  id: "$COLLECTOR"

  units:
$UNITS
  # Замки отпускаются по аварии всегда: из задымлённого зала должны
  # выйти.
  locks: [gpio-5]
$RUBBLE
  sound:
    player: ["aplay", "-q", "%s"]
    files:
      success: "/opt/mchs/sounds/success.wav"
      alarm: "/opt/mchs/sounds/alarm.wav"
EOF
chmod 640 "$ROOT/etc/mchs/config.yml"
say "конфигурация: /etc/mchs/config.yml"

cat > "$ROOT/etc/systemd/system/mchs-collector.service" <<EOF
# Создано tools/provision-card.sh
[Unit]
Description=Сборщик телеметрии МЧС ($SITE)
Documentation=file:///etc/mchs/config.yml
After=network-online.target
Wants=network-online.target

[Service]
Type=exec
User=$SERVICE_USER
Group=$SERVICE_USER
SupplementaryGroups=gpio i2c

ExecStart=/opt/mchs/bin/collector -config /etc/mchs/config.yml

Restart=always
RestartSec=2s

# Буфер телеметрии. Каталог создаёт systemd с нужным владельцем, так
# что подбирать uid заранее не требуется.
StateDirectory=mchs
WorkingDirectory=/var/lib/mchs

KillSignal=SIGTERM
TimeoutStopSec=30s

# PrivateDevices именно no: он подменяет /dev минимальным набором, и
# /dev/gpiochip0 внутрь не попадает — реле стало бы недоступно.
PrivateDevices=no
DevicePolicy=closed
DeviceAllow=/dev/gpiochip0 rw
DeviceAllow=/dev/i2c-1 rw

NoNewPrivileges=yes
ProtectSystem=strict
ProtectHome=yes
PrivateTmp=yes
ProtectKernelTunables=yes
ProtectKernelModules=yes
ProtectControlGroups=yes
ProtectClock=yes
RestrictNamespaces=yes
RestrictRealtime=yes
RestrictSUIDSGID=yes
LockPersonality=yes
MemoryDenyWriteExecute=yes
RestrictAddressFamilies=AF_INET AF_INET6 AF_UNIX

[Install]
WantedBy=multi-user.target
EOF
chmod 644 "$ROOT/etc/systemd/system/mchs-collector.service"
ln -sfn /etc/systemd/system/mchs-collector.service \
    "$ROOT/etc/systemd/system/multi-user.target.wants/mchs-collector.service"
say "служба mchs-collector включена"

# --- прошивка ---------------------------------------------------------

step "Прошивка"

# Шина датчика и начальные состояния выводов. Выводы задаются здесь, а
# не только в службе: до её запуска линия находится в режиме входа, и
# реле щёлкает при каждой загрузке. Уровень физический — прошивка про
# инверсию не знает, и dh (HIGH) при active_low означает разомкнутое
# реле.
marker_begin="# BEGIN mchs collector"
marker_end="# END mchs collector"
cfg="$BOOT/config.txt"

tmp=$(mktemp)
awk -v b="$marker_begin" -v e="$marker_end" '
    $0==b {skip=1} !skip {print} $0==e {skip=0}
' "$cfg" > "$tmp"

{
    cat "$tmp"
    echo "$marker_begin"
    echo "dtparam=i2c_arm=on"
    # Линии берутся из конфигурации сборщика: один источник вместо двух
    # списков, которые разъезжаются.
    grep -oP '^\s+line:\s*\K[0-9]+' "$ROOT/etc/mchs/config.yml" |
        while read -r line; do echo "gpio=$line=op,dh"; done
    echo "$marker_end"
} > "$cfg"
rm -f "$tmp"
say "в config.txt: i2c и $(grep -c '^gpio=' "$cfg") вывода"

# --- на диск ----------------------------------------------------------

sync
step "Готово"
say "извлеките карту, вставьте в Pi и включите питание"
say "вход: ssh $LOGIN@${IP%%/*}"
say "проверка: curl -s http://${IP%%/*}:8080/api/v1/status"
