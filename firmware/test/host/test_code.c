/*
 * Хостовые тесты проверки кода со стойки.
 *
 * Плата отвечает только «верно/неверно», но ответ должен быть честным:
 * набор не должен слипаться с чужим, а подбор — проходить незаметно.
 */
#include <string.h>

#include "code.h"
#include "testing.h"

int tests_failed;

static void enter(const char *digits)
{
    for (const char *p = digits; *p; p++) {
        code_feed(*p);
    }
}

static enum code_result submit(const char *digits)
{
    enter(digits);
    return code_feed(CODE_KEY_ENTER);
}

static void reset_all(void)
{
    fake_set_time(100000);
    code_init();
}

static void test_верный_код(void)
{
    reset_all();

    CHECK(submit("1234") == CODE_VALID, "верный код не принят");
    CHECK(code_entered() == 0, "буфер не очищен после проверки");
}

static void test_неверный_код(void)
{
    reset_all();

    CHECK(submit("9999") == CODE_INVALID, "неверный код принят");
    CHECK(code_entered() == 0, "буфер не очищен после отказа");
}

/* Набранное стирается клавишей '*'. */
static void test_стирание(void)
{
    reset_all();

    enter("12");
    CHECK(code_entered() == 2, "цифры не набираются");
    CHECK(code_feed(CODE_KEY_CLEAR) == CODE_CLEARED, "стирание не подтверждено");
    CHECK(code_entered() == 0, "буфер не очищен");

    CHECK(submit("1234") == CODE_VALID, "после стирания код не принимается");
}

/* Цифры, набранные кем-то раньше, не должны дописываться к новому вводу. */
static void test_недобранный_код_стирается_сам(void)
{
    reset_all();

    enter("12");
    fake_advance(CODE_ENTRY_TIMEOUT_MS + 1U);
    code_poll();

    CHECK(code_entered() == 0, "недобранный код не стёрся по тайм-ауту");
    CHECK(submit("1234") == CODE_VALID, "остаток прошлого набора исказил ввод");
}

/* Четырёхзначный код иначе подбирается за считаные минуты. */
static void test_подбор_придерживается_паузой(void)
{
    reset_all();

    for (int i = 0; i < CODE_MAX_ATTEMPTS - 1; i++) {
        CHECK(submit("0000") == CODE_INVALID, "попытка %d: ожидался отказ", i);
    }

    CHECK(submit("0000") == CODE_LOCKED, "после серии неудач паузы нет");
    CHECK(code_is_locked(), "пауза не отмечена");

    /* Даже верный код во время паузы не проверяется. */
    CHECK(submit("1234") == CODE_LOCKED, "во время паузы код проверяется");

    fake_advance(CODE_LOCKOUT_MS + 1U);
    CHECK(!code_is_locked(), "пауза не закончилась");
    CHECK(submit("1234") == CODE_VALID, "после паузы верный код не принят");
}

/* Верный ввод сбрасывает счётчик неудач: иначе человек, ошибшийся
 * дважды за смену, получал бы паузу на ровном месте. */
static void test_успех_сбрасывает_счётчик(void)
{
    reset_all();

    submit("0000");
    submit("1234");

    for (int i = 0; i < CODE_MAX_ATTEMPTS - 1; i++) {
        CHECK(submit("0000") == CODE_INVALID, "счётчик неудач не сброшен");
    }
}

static void test_смена_кода(void)
{
    reset_all();

    CHECK(code_set("4321"), "годный код отклонён");
    CHECK(submit("4321") == CODE_VALID, "новый код не принят");
    CHECK(submit("1234") == CODE_INVALID, "старый код всё ещё подходит");
}

static void test_негодный_код_не_применяется(void)
{
    reset_all();

    CHECK(!code_set("12"), "код из двух символов принят");
    CHECK(!code_set("12a4"), "код с буквой принят");
    CHECK(!code_set("1234567890123"), "слишком длинный код принят");
    CHECK(!code_set(NULL), "пустой указатель принят");

    CHECK(submit("1234") == CODE_VALID, "негодная смена испортила действующий код");
}

/* Лишние цифры не должны ломать разбор: просто не попадают в набор. */
static void test_переполнение_набора(void)
{
    reset_all();

    enter("123456789012345");
    CHECK(code_entered() == CODE_MAX_LEN, "буфер вышел за предел: %u",
          code_entered());
    CHECK(code_feed(CODE_KEY_ENTER) == CODE_INVALID, "переполненный набор принят за верный");
}

/* Нецифровые клавиши к коду отношения не имеют. */
static void test_буквы_игнорируются(void)
{
    reset_all();

    enter("12");
    code_feed('A');
    code_feed('D');
    enter("34");

    CHECK(code_entered() == 4, "буквы попали в набор: %u", code_entered());
    CHECK(code_feed(CODE_KEY_ENTER) == CODE_VALID, "буквы исказили набор");
}

/* Подтверждение пустого набора — не отказ: человек просто нажал '#'. */
static void test_пустое_подтверждение(void)
{
    reset_all();

    CHECK(code_feed(CODE_KEY_ENTER) == CODE_PENDING, "пустой ввод засчитан как попытка");

    for (int i = 0; i < CODE_MAX_ATTEMPTS + 2; i++) {
        code_feed(CODE_KEY_ENTER);
    }
    CHECK(!code_is_locked(), "пустые нажатия привели к паузе");
}

int main(void)
{
    RUN(test_верный_код);
    RUN(test_неверный_код);
    RUN(test_стирание);
    RUN(test_недобранный_код_стирается_сам);
    RUN(test_подбор_придерживается_паузой);
    RUN(test_успех_сбрасывает_счётчик);
    RUN(test_смена_кода);
    RUN(test_негодный_код_не_применяется);
    RUN(test_переполнение_набора);
    RUN(test_буквы_игнорируются);
    RUN(test_пустое_подтверждение);

    if (tests_failed) {
        printf("\nПРОВАЛЕНО проверок: %d\n", tests_failed);
        return 1;
    }
    printf("\nВСЕ ПРОВЕРКИ ПРОЙДЕНЫ\n");
    return 0;
}
