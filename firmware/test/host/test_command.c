/*
 * Хостовые тесты командного тракта стойки.
 *
 * Стойка принимает две команды: сменить код и задать период отчётов.
 * Проверяется то, что ломается молча: повтор команды, порядок при
 * переотправке и отказ от чужих команд — плата не должна исполнять
 * то, что адресовано другому устройству.
 */
#include <string.h>

#include "code.h"
#include "command.h"
#include "testing.h"

int tests_failed;

static void reset_all(void)
{
    fake_set_time(100000);
    command_reset();
    code_init();
}

static struct command make(enum command_kind kind, const char *id, uint32_t seq)
{
    struct command c;
    memset(&c, 0, sizeof(c));
    c.kind = kind;
    c.seq = seq;
    strcpy(c.id, id);
    return c;
}

static void test_смена_кода(void)
{
    reset_all();

    struct command c = make(CMD_SET_CODE, "c1", 1);
    strcpy(c.code, "4731");

    struct command_ack ack;
    command_apply(&c, &ack);
    CHECK(ack.accepted, "смена кода отклонена: %s", ack.reason);

    CHECK(code_feed('4') == CODE_PENDING, "первая цифра дала ответ");
    code_feed('7');
    code_feed('3');
    code_feed('1');
    CHECK(code_feed(CODE_KEY_ENTER) == CODE_VALID, "новый код не принят");
}

static void test_негодный_код_отклоняется(void)
{
    reset_all();

    struct command c = make(CMD_SET_CODE, "c2", 1);
    strcpy(c.code, "12"); /* короче трёх цифр */

    struct command_ack ack;
    command_apply(&c, &ack);
    CHECK(!ack.accepted, "код из двух цифр принят");
    CHECK(strcmp(ack.reason, "bad_code") == 0, "причина отказа: %s", ack.reason);

    /* Прежний код продолжает действовать: отказ не должен оставлять
     * стойку без кода вовсе. */
    code_feed('1');
    code_feed('2');
    code_feed('3');
    code_feed('4');
    CHECK(code_feed(CODE_KEY_ENTER) == CODE_VALID, "прежний код перестал действовать");
}

static void test_повтор_команды_не_применяется_дважды(void)
{
    reset_all();

    struct command first = make(CMD_SET_CODE, "dup", 1);
    strcpy(first.code, "4731");

    struct command_ack ack;
    command_apply(&first, &ack);
    CHECK(ack.accepted, "первая команда отклонена: %s", ack.reason);

    /* Сборщик переотправляет, не дождавшись подтверждения. Повтор
     * подтверждается, но код второй раз не ставится — иначе смена
     * пришлась бы на середину набора. */
    command_apply(&first, &ack);
    CHECK(ack.accepted, "повтор не подтверждён");
    CHECK(strcmp(ack.reason, "duplicate") == 0, "повтор не распознан: %s",
          ack.reason);
}

static void test_период_отчётов(void)
{
    reset_all();

    struct command c = make(CMD_SET_PERIOD, "p1", 1);
    c.period_ms = 500U;

    struct command_ack ack;
    command_apply(&c, &ack);
    CHECK(ack.accepted, "период отклонён: %s", ack.reason);
    CHECK(command_period_ms() == 500U, "период не применён: %lu",
          (unsigned long)command_period_ms());

    struct command bad = make(CMD_SET_PERIOD, "p2", 2);
    bad.period_ms = 5U;
    command_apply(&bad, &ack);
    CHECK(!ack.accepted, "период 5 мс принят");
}

static void test_устаревшая_команда(void)
{
    reset_all();

    struct command newer = make(CMD_SET_PERIOD, "p-2", 10);
    newer.period_ms = 500U;
    struct command_ack ack;
    command_apply(&newer, &ack);

    struct command older = make(CMD_SET_PERIOD, "p-1", 3);
    older.period_ms = 1000U;
    command_apply(&older, &ack);

    CHECK(!ack.accepted, "команда из прошлого применена");
    CHECK(command_period_ms() == 500U, "период изменён устаревшей командой");
}

static void test_разбор_команд(void)
{
    struct command c;
    const char *set_code = "cmd=set_code id=a1 seq=7 code=4731";

    CHECK(command_parse(set_code, (uint16_t)strlen(set_code), &c),
          "set_code не разобран");
    CHECK(c.kind == CMD_SET_CODE, "вид команды разобран неверно");
    CHECK(strcmp(c.code, "4731") == 0, "код разобран неверно: %s", c.code);

    const char *period = "cmd=set_period id=a2 seq=8 period_ms=200";
    CHECK(command_parse(period, (uint16_t)strlen(period), &c),
          "set_period не разобран");
    CHECK(c.period_ms == 200U, "период: %lu", (unsigned long)c.period_ms);

    CHECK(!command_parse("мусор", 10, &c), "мусор принят за команду");
    CHECK(!command_parse("cmd=set_code", 12, &c), "команда без id принята");
}

/* Команды чужих устройств плата не исполняет: дым, свет и замки висят
 * на сборщике помещения, а препятствия — на своей плате. */
static void test_чужие_команды_отклоняются(void)
{
    struct command c;

    const char *cases[] = {
        "cmd=heater_on id=a1 seq=1",
        "cmd=drop id=a2 seq=2 obstacle=1",
        "cmd=equipment id=a3 seq=3 unit=smoke state=on",
        "cmd=alarm id=a4 seq=4",
    };

    for (unsigned i = 0; i < sizeof(cases) / sizeof(cases[0]); i++) {
        CHECK(!command_parse(cases[i], (uint16_t)strlen(cases[i]), &c),
              "чужая команда принята: %s", cases[i]);
    }
}

/* Длинный код в команде не должен переполнить буфер платы. */
static void test_слишком_длинный_код(void)
{
    struct command c;
    const char *raw = "cmd=set_code id=a1 seq=1 code=12345678901234567890";

    CHECK(!command_parse(raw, (uint16_t)strlen(raw), &c),
          "код длиннее буфера принят");
}

static void test_очередь_команд(void)
{
    reset_all();

    struct command c = make(CMD_SET_PERIOD, "q1", 1);
    c.period_ms = 300U;
    CHECK(command_push(&c), "команда не легла в очередь");

    struct command out;
    CHECK(command_pop(&out), "очередь пуста сразу после записи");
    CHECK(out.period_ms == 300U, "из очереди пришло не то: %lu",
          (unsigned long)out.period_ms);
    CHECK(!command_pop(&out), "очередь отдала лишнее");
}

int main(void)
{
    RUN(test_смена_кода);
    RUN(test_негодный_код_отклоняется);
    RUN(test_повтор_команды_не_применяется_дважды);
    RUN(test_период_отчётов);
    RUN(test_устаревшая_команда);
    RUN(test_разбор_команд);
    RUN(test_чужие_команды_отклоняются);
    RUN(test_слишком_длинный_код);
    RUN(test_очередь_команд);

    return tests_failed == 0 ? 0 : 1;
}
