/*
 * Приём и исполнение команд от сборщика (ARCHITECTURE.md §7).
 *
 * Стойка принимает две команды: сменить код и задать период отчётов.
 * Оборудованием зала — дымом, светом, замками — ведает сборщик
 * помещения: у него реле, и решение исполняет тот, у кого они есть.
 *
 * Команды приходят по UDP на тот же порт, с которого уходит телеметрия,
 * парами ключ=значение через пробел:
 *
 *     cmd=set_code id=a1b2c5 seq=9 code=4731
 *     cmd=set_period id=a1b2c6 seq=10 period_ms=200
 *     cmd=fire id=a1b2c7 seq=11 state=on
 *
 * JSON здесь не используется намеренно: разбор JSON на плате стоит
 * дороже, чем весь командный тракт, а формат ключ=значение читается
 * линейным проходом по строке.
 */
#ifndef COMMAND_H
#define COMMAND_H

#include <stdbool.h>
#include <stdint.h>

/* Потолок длины команды на проводе; совпадает с MaxCommandLen в Go. */
#define COMMAND_MAX_LEN 160

/* Длина идентификатора команды вместе с завершающим нулём. */
#define COMMAND_ID_LEN 17

/* Период отчётов телеметрии по умолчанию, мс. */
#define COMMAND_DEFAULT_PERIOD_MS 200U

/* Глубина очереди принятых команд. Callback стека их только складывает,
 * исполняет главный цикл — очередь покрывает всплеск. */
#define COMMAND_QUEUE_LEN 8

enum command_kind {
    CMD_NONE = 0,
    CMD_SET_PERIOD, /* период отчётов телеметрии */
    CMD_SET_CODE,   /* сменить код, набираемый на клавиатуре */
    CMD_FIRE,       /* зажечь или потушить имитатор очага */
};

/* Длина кода вместе с завершающим нулём; совпадает с CODE_MAX_LEN. */
#define COMMAND_CODE_LEN 13

struct command {
    enum command_kind kind;
    char id[COMMAND_ID_LEN];
    uint32_t seq;
    uint32_t period_ms;
    char code[COMMAND_CODE_LEN];
    /* Состояние для cmd=fire: true — зажечь. */
    bool on;
};

/* Результат исполнения: уходит обратно подтверждением. */
struct command_ack {
    char id[COMMAND_ID_LEN];
    bool accepted;
    const char *reason;
    const char *state;
    uint32_t uptime_ms;
};

/* Разобрать команду из строки. false — строка не команда или испорчена. */
bool command_parse(const char *raw, uint16_t len, struct command *out);

/* Положить команду в очередь. Вызывается из callback стека.
 * false — очередь переполнена, команда отброшена. */
bool command_push(const struct command *cmd);

/* Забрать следующую команду из очереди. false — очередь пуста.
 * Вызывается из главного цикла. */
bool command_pop(struct command *out);

/* Исполнить команду и заполнить подтверждение. */
void command_apply(const struct command *cmd, struct command_ack *ack);

/* Собрать подтверждение в строку для отправки. Возвращает длину. */
int command_format_ack(const struct command_ack *ack, char *buf, int size);

/* Действующий период отчётов. Плата не хранит настройки между
 * перезапусками: после старта действуют значения из прошивки, пока
 * сборщик не дошлёт свои (ARCHITECTURE.md §7.5). */
uint32_t command_period_ms(void);

/* Сбросить состояние приёма команд (для тестов). */
void command_reset(void);

#endif /* COMMAND_H */
