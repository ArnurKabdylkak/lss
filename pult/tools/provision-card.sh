#!/usr/bin/env bash
# Подготовка карты пульта напрямую, без ansible.
#
# Нужно, когда узла ещё нет в сети: ansible требует SSH, а SSH требует
# настроенной карты. Скрипт закрывает именно этот разрыв — ставит
# доступ, сеть и саму службу на смонтированную файловую систему.
# Дальнейшие обновления идут штатно, плейбуком.
#
#   sudo tools/provision-card.sh                      # пульт, .13
#   sudo tools/provision-card.sh 192.168.1.42/24      # другой адрес
#   sudo EXTRA_IP=192.168.0.4/24 tools/provision-card.sh
#
# Второй аргумент — открытый ключ, если он лежит не в ~/.ssh/id_rsa.pub.
#
# Пароль учётной записи задаётся переменной PASS. Он не пишется ни в
# репозиторий, ни в журнал — только в /etc/shadow карты:
#
#   sudo PASS='...' tools/provision-card.sh
#
# Без PASS пароль остаётся запертым, и вход возможен только по ключу.
#
# Запись идёт в /etc и /opt карты, поэтому нужен root. Повторный запуск
# безопасен: все правки идемпотентны.
set -euo pipefail

# --- что и куда -------------------------------------------------------

ROOT=${ROOT:-/run/media/$SUDO_USER/rootfs}
BOOT=${BOOT:-/run/media/$SUDO_USER/bootfs}

# Адреса по network-plan.xlsx: сеть плоская, 192.168.1.0/24, шлюз .1,
# центр .2, сборщики .10-.12. Пульту отведён .13.
IP=${1:-${IP:-192.168.1.13/24}}
GW=${GW:-192.168.1.1}
DNS=${DNS:-$GW}
EXTRA_IP=${EXTRA_IP:-}

CENTER=${CENTER:-http://192.168.1.2:8080}
NTP=${NTP:-192.168.1.2}

# Имя хоста в одном ряду со сборщиками: rpi_room1…3 — комнаты,
# rpi_pult — пульт. Имена в этом контуре никто не разрешает, но они
# видны в журналах и в приглашении консоли.
HOST=${HOST:-rpi_pult}

# Пользователь для входа.
LOGIN=${LOGIN:-pult}
PASS=${PASS:-}
PUBKEY=${2:-${PUBKEY:-/home/$SUDO_USER/.ssh/id_rsa.pub}}

BINARY=${BINARY:-bin/pultd-arm64}
SERVICE_USER=mchs
INSTALL_DIR=/opt/mchs
CONFIG_DIR=/etc/mchs

say() { printf '  %s\n' "$*"; }
step() { printf '\n== %s\n' "$*"; }

# --- проверки ---------------------------------------------------------

[[ $EUID -eq 0 ]] || { echo "нужен root: sudo $0" >&2; exit 1; }
[[ -n ${SUDO_USER:-} ]] || { echo "запускать через sudo, не из-под root напрямую" >&2; exit 1; }

for p in "$ROOT/etc/os-release" "$BOOT/config.txt"; do
    [[ -e $p ]] || { echo "не похоже на карту Raspberry Pi: нет $p" >&2; exit 1; }
done

cd "$(dirname "$0")/.."
if [[ ! -f $BINARY ]]; then
    echo "  бинаря $BINARY нет, собираю под aarch64…"
    sudo -u "$SUDO_USER" env CGO_ENABLED=0 GOOS=linux GOARCH=arm64 \
        go build -trimpath -ldflags="-s -w" -o "$BINARY" ./cmd/pultd \
        || { echo "сборка не удалась — соберите вручную: make build-arm64" >&2; exit 1; }
fi
[[ -f $PUBKEY ]] || { echo "нет открытого ключа $PUBKEY" >&2; exit 1; }

# Секреты службы. Без них пульт не войдёт в центр и не сможет
# остановить помещения напрямую, поэтому спрашиваем сразу, а не после
# первой неудачной попытки в зале.
PULT_PASS=${PULT_PASS:-}
PULT_ALARM_TOKEN=${PULT_ALARM_TOKEN:-}
[[ -n $PULT_PASS ]] || {
    echo "не задан PULT_PASS — пароль учётной записи pult в центре" >&2; exit 1; }
[[ -n $PULT_ALARM_TOKEN ]] || {
    echo "не задан PULT_ALARM_TOKEN — секрет прямой аварии (тот же, что у сборщиков)" >&2
    exit 1; }

# --- доступ -----------------------------------------------------------

step "Доступ"

install -d -m 755 "$ROOT/etc/systemd/system/multi-user.target.wants"
ln -sfn /lib/systemd/system/ssh.service \
    "$ROOT/etc/systemd/system/multi-user.target.wants/ssh.service"
say "ssh.service включён"

home=$(awk -F: -v u="$LOGIN" '$1==u {print $6}' "$ROOT/etc/passwd")
if [[ -z $home ]]; then
    # В чистом образе есть только pi. Заводим учётную запись пульта
    # прямо в файлах карты: подобрать свободный uid здесь можно —
    # образ пустой, и кроме pi (1000) в нём никого нет.
    uid=1001
    while awk -F: -v i="$uid" '$3==i {found=1} END {exit !found}' "$ROOT/etc/passwd"; do
        uid=$((uid + 1))
    done
    home=/home/$LOGIN

    echo "$LOGIN:x:$uid:$uid:Пульт управления:$home:/bin/bash" >> "$ROOT/etc/passwd"
    echo "$LOGIN:!:20000:0:99999:7:::" >> "$ROOT/etc/shadow"
    echo "$LOGIN:x:$uid:" >> "$ROOT/etc/group"

    # Группы доступа к железу: без i2c пульт не достучится до
    # расширителей, остальные — как у штатного pi.
    for g in adm dialout sudo audio video plugdev users input render netdev spi i2c gpio; do
        grep -q "^$g:" "$ROOT/etc/group" || continue
        sed -i "s/^\($g:[^:]*:[^:]*:\)\(.*\)$/\1\2,$LOGIN/; s/^\($g:[^:]*:[^:]*:\),/\1/" \
            "$ROOT/etc/group"
    done
    install -d -m 755 -o "$uid" -g "$uid" "$ROOT$home"
    say "заведена учётная запись $LOGIN (uid $uid)"
fi
uid=$(awk -F: -v u="$LOGIN" '$1==u {print $3}' "$ROOT/etc/passwd")
gid=$(awk -F: -v u="$LOGIN" '$1==u {print $4}' "$ROOT/etc/passwd")

# Пароль для консоли. Хеш считается здесь же: сам пароль на карту не
# попадает, в журнал скрипта — тоже.
if [[ -n $PASS ]]; then
    hash=$(openssl passwd -6 "$PASS")
    # Разделитель | — в хеше встречаются / и точки, но не вертикальная
    # черта.
    sed -i "s|^$LOGIN:[^:]*:|$LOGIN:$hash:|" "$ROOT/etc/shadow"
    say "пароль $LOGIN задан (хеш в /etc/shadow карты)"
else
    say "пароль $LOGIN оставлен запертым: вход только по ключу"
fi

install -d -m 700 -o "$uid" -g "$gid" "$ROOT$home/.ssh"
install -m 600 -o "$uid" -g "$gid" "$PUBKEY" "$ROOT$home/.ssh/authorized_keys"
say "ключ положен в $home/.ssh/authorized_keys"

sed -i "s|^$LOGIN:\(.*\):/usr/sbin/nologin$|$LOGIN:\1:/bin/bash|" "$ROOT/etc/passwd"

# sudo без пароля: плейбук поднимает права неинтерактивно.
cat > "$ROOT/etc/sudoers.d/010-mchs" <<EOF
# Создано tools/provision-card.sh
$LOGIN ALL=(ALL) NOPASSWD:ALL
EOF
chmod 440 "$ROOT/etc/sudoers.d/010-mchs"
say "sudo без пароля для $LOGIN"

# --- сеть -------------------------------------------------------------

step "Сеть"

conn="$ROOT/etc/NetworkManager/system-connections/mchs-ethernet.nmconnection"
install -d -m 755 "$ROOT/etc/NetworkManager/system-connections"

# Чужие профили на том же интерфейсе убираем: два профиля на eth0 —
# верный способ получить непредсказуемый адрес после перезагрузки.
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
chmod 600 "$conn"
say "профиль mchs-ethernet на eth0: $IP, шлюз $GW${EXTRA_IP:+, плюс $EXTRA_IP}"

echo "$HOST" > "$ROOT/etc/hostname"
# Имя у машины одно: прежние строки 127.0.1.1 убираем, иначе sudo и
# ssh разрешают имя во что придётся.
sed -i '/^127\.0\.1\.1[[:space:]]/d' "$ROOT/etc/hosts"
printf '127.0.1.1\t%s\n' "$HOST" >> "$ROOT/etc/hosts"
say "имя хоста: $HOST"

# --- время ------------------------------------------------------------

step "Время"

# У Raspberry Pi нет часов с батарейкой: после обесточивания время
# уезжает, и журнал нажатий становится бесполезен для разбора.
install -d -m 755 "$ROOT/etc/systemd/timesyncd.conf.d"
cat > "$ROOT/etc/systemd/timesyncd.conf.d/mchs.conf" <<EOF
# Создано tools/provision-card.sh
[Time]
NTP=$NTP
EOF
ln -sfn /lib/systemd/system/systemd-timesyncd.service \
    "$ROOT/etc/systemd/system/sysinit.target.wants/systemd-timesyncd.service" 2>/dev/null || true
say "сервер времени: $NTP"

# --- шина I2C ---------------------------------------------------------

step "Шина"

# Включается двумя вещами: dtparam поднимает контроллер, а узел
# /dev/i2c-1 создаёт модуль i2c-dev. Без модуля расширители не видны,
# хотя в дереве устройств шина значится рабочей — ровно этот случай
# выглядит как «пульт не находит микросхемы».
if grep -q '^#\?dtparam=i2c_arm=' "$BOOT/config.txt"; then
    sed -i 's|^#\?dtparam=i2c_arm=.*|dtparam=i2c_arm=on|' "$BOOT/config.txt"
else
    printf '\n# Расширители пульта MCP23017\ndtparam=i2c_arm=on\n' >> "$BOOT/config.txt"
fi
install -d -m 755 "$ROOT/etc/modules-load.d"
cat > "$ROOT/etc/modules-load.d/mchs-i2c.conf" <<'EOF'
# Создано tools/provision-card.sh: узлы /dev/i2c-* для расширителей.
i2c-dev
EOF
say "I2C включён в прошивке, модуль i2c-dev грузится при старте"

# --- служба -----------------------------------------------------------

step "Служба"

# Учётная запись службы: права root пульту не нужны, доступ к шине
# даётся группой.
if ! grep -q "^$SERVICE_USER:" "$ROOT/etc/passwd"; then
    svc_uid=900
    while awk -F: -v i="$svc_uid" '$3==i {found=1} END {exit !found}' "$ROOT/etc/passwd"; do
        svc_uid=$((svc_uid + 1))
    done
    echo "$SERVICE_USER:x:$svc_uid:$svc_uid:Служба пульта:/nonexistent:/usr/sbin/nologin" \
        >> "$ROOT/etc/passwd"
    echo "$SERVICE_USER:!:20000:0:99999:7:::" >> "$ROOT/etc/shadow"
    echo "$SERVICE_USER:x:$svc_uid:" >> "$ROOT/etc/group"
    sed -i "s/^\(i2c:[^:]*:[^:]*:\)\(.*\)$/\1\2,$SERVICE_USER/; s/^\(i2c:[^:]*:[^:]*:\),/\1/" \
        "$ROOT/etc/group"
    say "заведена учётная запись службы $SERVICE_USER (uid $svc_uid)"
fi
svc_uid=$(awk -F: -v u="$SERVICE_USER" '$1==u {print $3}' "$ROOT/etc/passwd")
svc_gid=$(awk -F: -v u="$SERVICE_USER" '$1==u {print $4}' "$ROOT/etc/passwd")

install -d -m 755 "$ROOT$INSTALL_DIR/bin"
install -m 755 "$BINARY" "$ROOT$INSTALL_DIR/bin/pultd"
install -d -m 750 -g "$svc_gid" "$ROOT$CONFIG_DIR"
say "бинарь положен в $INSTALL_DIR/bin/pultd"

# Раскладка: ровно та, что в deploy/group_vars. Дальше её ведёт
# плейбук, здесь — первая рабочая копия.
install -m 640 -g "$svc_gid" /dev/null "$ROOT$CONFIG_DIR/pult.yml"
cat > "$ROOT$CONFIG_DIR/pult.yml" <<EOF
# Создано tools/provision-card.sh. Дальше файл ведёт плейбук:
# правки руками затрутся при следующей выкладке.

center:
  url: "$CENTER"
  login: "pult"
  password: "\${PULT_PASS}"
  timeout: 3s

i2c:
  device: /dev/i2c-1

timing:
  scan: 5ms
  debounce: 30ms
  poll: 500ms
  blink: 400ms

emergency:
  addr: "0x20"
  button: B7
  lamp: A7
  normally_closed: false
  reason: "аварийная кнопка на пульте"

lamps_source_voltage: true

rooms:
  # Порядок кнопок одинаков во всех комнатах, насколько позволяет
  # оборудование: освещение, пушка, вытяжка — всегда B0, B1, B2.
  # Так оператор не ищет кнопку глазами, переходя от зала к залу.
  - id: room-1
    name: "Теплокамера"
    addr: "0x20"
    units:
      - {unit: light,  name: "Освещение",            button: B0, lamp: A0}
      - {unit: heat,   name: "Тепловая пушка",       button: B1, lamp: A1}
      - {unit: fan,    name: "Вытяжка",              button: B2, lamp: A2}
      - {unit: stairs, name: "Бесконечная лестница", button: B3, lamp: A3}

  - id: room-2
    name: "Дымокамера, комната 1"
    addr: "0x22"
    units:
      - {unit: light,  name: "Освещение",      button: B0, lamp: A0}
      - {unit: heat,   name: "Тепловая пушка", button: B1, lamp: A1}
      - {unit: fan,    name: "Вытяжка",        button: B2, lamp: A2}
      - {unit: smoke,  name: "Дымогенератор",  button: B3, lamp: A3}
      - {unit: strobe, name: "Стробоскоп",     button: B4, lamp: A4}
      - {unit: sound,  name: "Аудиоэффекты",   button: B5, lamp: A5}
      - {unit: flood,  name: "Прожекторы",     button: B6, lamp: A6}

  - id: room-3
    name: "Дымокамера, комната 2"
    addr: "0x24"
    units:
      - {unit: light,  name: "Освещение",      button: B0, lamp: A0}
      - {unit: heat,   name: "Тепловая пушка", button: B1, lamp: A1}
      - {unit: fan,    name: "Вытяжка",        button: B2, lamp: A2}
      - {unit: smoke,  name: "Дымогенератор",  button: B3, lamp: A3}
      - {unit: strobe, name: "Стробоскоп",     button: B4, lamp: A4}
      - {unit: sound,  name: "Аудиоэффекты",   button: B5, lamp: A5}
      - {unit: rubble, name: "Завалы",         button: B6, lamp: A6}

direct_token: "\${PULT_ALARM_TOKEN}"
collectors:
  - {room: room-1, url: "http://192.168.1.10:8080"}
  - {room: room-2, url: "http://192.168.1.11:8080"}
  - {room: room-3, url: "http://192.168.1.12:8080"}
EOF
chmod 640 "$ROOT$CONFIG_DIR/pult.yml"
chgrp "$svc_gid" "$ROOT$CONFIG_DIR/pult.yml"
say "раскладка положена в $CONFIG_DIR/pult.yml"

# Секреты отдельным файлом: раскладку открывают при каждой правке
# схемы, а пароль с токеном — нет.
touch "$ROOT$CONFIG_DIR/pult.env"
chmod 640 "$ROOT$CONFIG_DIR/pult.env"
chgrp "$svc_gid" "$ROOT$CONFIG_DIR/pult.env"
cat > "$ROOT$CONFIG_DIR/pult.env" <<EOF
# Создано tools/provision-card.sh
PULT_PASS=$PULT_PASS
PULT_ALARM_TOKEN=$PULT_ALARM_TOKEN
EOF
say "секреты положены в $CONFIG_DIR/pult.env (0640)"

cat > "$ROOT/etc/systemd/system/mchs-pult.service" <<EOF
# Создано tools/provision-card.sh
[Unit]
Description=Пульт управления тренажёрным комплексом
Documentation=file://$CONFIG_DIR/pult.yml
After=network-online.target
Wants=network-online.target

[Service]
Type=exec
User=$SERVICE_USER
Group=$SERVICE_USER
SupplementaryGroups=i2c
EnvironmentFile=$CONFIG_DIR/pult.env
ExecStart=$INSTALL_DIR/bin/pultd -config $CONFIG_DIR/pult.yml

# Пульт без службы — мёртвые кнопки, поэтому поднимаем всегда.
Restart=always
RestartSec=2s

ProtectSystem=strict
ProtectHome=true
PrivateTmp=true
NoNewPrivileges=true

[Install]
WantedBy=multi-user.target
EOF
ln -sfn /etc/systemd/system/mchs-pult.service \
    "$ROOT/etc/systemd/system/multi-user.target.wants/mchs-pult.service"
say "служба mchs-pult включена"

step "Готово"
cat <<EOF
  Карта подготовлена. Дальше:
    1. Размонтировать карту и переставить в пульт.
    2. Включить и проверить:  ssh $LOGIN@${IP%%/*}
    3. Расширители на шине:   sudo /opt/mchs/bin/pultd -probe
    4. Журнал службы:         journalctl -u mchs-pult -f
  Дальнейшие обновления — плейбуком: make deploy
EOF
