#include <stdio.h>
#include <string.h>

#include "code.h"
#include "command.h"
#if FIRE_ROLE
#include "fire.h"
#endif
#include "systime.h"

/* Период отчётов. Уставки тепловой защиты здесь не хранятся: пушка и
 * её датчик — на стороне Raspberry Pi. */
static uint32_t period_ms = COMMAND_DEFAULT_PERIOD_MS;

/* Последний исполненный seq: защита от повтора. Сборщик переотправляет
 * команду, не дождавшись подтверждения, и применить её дважды нельзя. */
static uint32_t last_seq;

/* Идентификатор последней исполненной команды — чтобы отличить повтор
 * от новой команды. */
static char last_id[COMMAND_ID_LEN];

static struct command queue[COMMAND_QUEUE_LEN];
static volatile uint8_t queue_head;
static volatile uint8_t queue_tail;

uint32_t command_period_ms(void) { return period_ms; }

static bool str_eq(const char *a, const char *b)
{
    return strcmp(a, b) == 0;
}

/* Разбор беззнакового числа. Возвращает false при мусоре. */
static bool parse_u32(const char *s, uint32_t *out)
{
    uint32_t value = 0;
    if (*s == '\0') {
        return false;
    }
    for (; *s; s++) {
        if (*s < '0' || *s > '9') {
            return false;
        }
        value = value * 10U + (uint32_t)(*s - '0');
    }
    *out = value;
    return true;
}

bool command_parse(const char *raw, uint16_t len, struct command *out)
{
    if (raw == NULL || out == NULL || len == 0U || len >= COMMAND_MAX_LEN) {
        return false;
    }

    /* Копия нужна: разбор режет строку на пары, а приходящий буфер
     * принадлежит стеку и не завершён нулём. */
    char buf[COMMAND_MAX_LEN];
    memcpy(buf, raw, len);
    buf[len] = '\0';

    memset(out, 0, sizeof(*out));

    bool has_kind = false;
    bool has_id = false;

    char *save = NULL;
    for (char *tok = strtok_r(buf, " \t\r\n", &save); tok != NULL;
         tok = strtok_r(NULL, " \t\r\n", &save)) {

        char *eq = strchr(tok, '=');
        if (eq == NULL) {
            return false;
        }
        *eq = '\0';

        const char *key = tok;
        const char *value = eq + 1;

        if (str_eq(key, "cmd")) {
            if (str_eq(value, "fire")) {
                out->kind = CMD_FIRE;
            } else if (str_eq(value, "set_period")) {
                out->kind = CMD_SET_PERIOD;
            } else if (str_eq(value, "set_code")) {
                out->kind = CMD_SET_CODE;
            } else {
                return false;
            }
            has_kind = true;
        } else if (str_eq(key, "id")) {
            if (strlen(value) >= COMMAND_ID_LEN) {
                return false;
            }
            strcpy(out->id, value);
            has_id = true;
        } else if (str_eq(key, "seq")) {
            if (!parse_u32(value, &out->seq)) {
                return false;
            }
        } else if (str_eq(key, "period_ms")) {
            if (!parse_u32(value, &out->period_ms)) {
                return false;
            }
        } else if (str_eq(key, "state")) {
            /* Состояние очага. Чужие значения не угадываем: команда
             * с мусором в поле должна быть отвергнута, а не понята
             * наполовину. */
            if (str_eq(value, "on")) {
                out->on = true;
            } else if (str_eq(value, "off")) {
                out->on = false;
            } else {
                return false;
            }
        } else if (str_eq(key, "code")) {
            if (strlen(value) >= COMMAND_CODE_LEN) {
                return false;
            }
            strcpy(out->code, value);
        }
        /* Незнакомые ключи игнорируются: центр может добавить поле
         * раньше, чем обновят прошивку. */
    }

    return has_kind && has_id;
}

bool command_push(const struct command *cmd)
{
    uint8_t next = (uint8_t)((queue_head + 1U) % COMMAND_QUEUE_LEN);
    if (next == queue_tail) {
        return false; /* очередь полна */
    }

    queue[queue_head] = *cmd;
    queue_head = next;
    return true;
}

bool command_pop(struct command *out)
{
    if (queue_tail == queue_head) {
        return false;
    }

    *out = queue[queue_tail];
    queue_tail = (uint8_t)((queue_tail + 1U) % COMMAND_QUEUE_LEN);
    return true;
}

void command_apply(const struct command *cmd, struct command_ack *ack)
{
    memset(ack, 0, sizeof(*ack));
    strcpy(ack->id, cmd->id);
    ack->uptime_ms = millis();
    ack->accepted = false;
    ack->reason = "ok";
    ack->state = "ok";

    /* Повтор уже исполненной команды безвреден, но применять её второй
     * раз нельзя: сборщик переотправляет, не дождавшись ответа. Смена
     * кода повторно — это ещё и новый код посреди набора. */
    if (cmd->id[0] != '\0' && strcmp(cmd->id, last_id) == 0) {
        ack->accepted = true;
        ack->reason = "duplicate";
        return;
    }
    /* Команда из прошлого: сборщик мог перепутать порядок при повторе. */
    if (cmd->seq != 0U && cmd->seq < last_seq) {
        ack->reason = "stale_seq";
        return;
    }

    switch (cmd->kind) {
    case CMD_SET_PERIOD:
        if (cmd->period_ms < 50U || cmd->period_ms > 60000U) {
            ack->reason = "out_of_range";
        } else {
            period_ms = cmd->period_ms;
            ack->accepted = true;
        }
        break;

    case CMD_SET_CODE:
        /* Проверку длины и состава делает сам модуль кода: плата не
         * полагается на валидацию, сделанную где-то ещё. */
        if (!code_set(cmd->code)) {
            ack->reason = "bad_code";
        } else {
            ack->accepted = true;
        }
        break;

    case CMD_FIRE:
#if FIRE_ROLE
        fire_set(cmd->on);
        ack->state = cmd->on ? "on" : "off";
        ack->accepted = true;
#else
        /* Плата этой роли очагом не управляет: команда адресована
         * не ей, и исполнять её молча нельзя. */
        ack->reason = "not_a_fire";
#endif
        break;

    case CMD_NONE:
    default:
        ack->reason = "unknown";
        break;
    }

    if (ack->accepted) {
        strcpy(last_id, cmd->id);
        if (cmd->seq > last_seq) {
            last_seq = cmd->seq;
        }
    }
}

int command_format_ack(const struct command_ack *ack, char *buf, int size)
{
    return snprintf(buf, (size_t)size,
                    "ack=%s ok=%d state=%s reason=%s uptime_ms=%lu",
                    ack->id, ack->accepted ? 1 : 0,
                    ack->state != NULL ? ack->state : "unknown",
                    ack->reason != NULL ? ack->reason : "ok",
                    (unsigned long)ack->uptime_ms);
}

void command_reset(void)
{
    period_ms = COMMAND_DEFAULT_PERIOD_MS;
    last_seq = 0U;
    last_id[0] = '\0';
    queue_head = 0U;
    queue_tail = 0U;
}
