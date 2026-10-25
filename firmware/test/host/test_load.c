/*
 * Хостовые тесты счётчика ударного молота.
 *
 * Здесь ошибка стоит дорого в обе стороны: лишние повторения зачтут
 * норму, которой не было, а потерянные заставят человека работать
 * вдвое. Поэтому правило «два прохода геркона — одно повторение»
 * проверяется до того, как попадёт на тренажёр.
 */
#include <libopencm3/stm32/gpio.h>

#include "load.h"
#include "testing.h"

int tests_failed;

/*
 * Смена уровня на герконе так, как её видит плата: сначала опрос
 * замечает новый уровень, затем, выдержав время, принимает его.
 * Одного опроса мало — на том и держится подавление дребезга.
 */
static bool settle(int closed)
{
    fake_input_level = closed ? 0 : 0xFFFF;

    load_poll(); /* заметили смену, начали отсчёт */
    fake_advance(LOAD_DEBOUNCE_MS + 1);
    return load_poll(); /* приняли */
}

/* Один проход магнита мимо геркона: подошёл и ушёл. */
static bool pass_reed(void)
{
    bool counted = settle(1);
    settle(0);
    return counted;
}

/* Полный цикл молота: магнит проходит мимо геркона дважды. */
static bool full_rep(void)
{
    bool first = pass_reed();
    bool second = pass_reed();
    return first || second;
}

static void reset_all(void)
{
    fake_set_time(100000);
    fake_input_level = 0xFFFF;
    load_init();
}

/* Один проход — ещё не повторение: магнит только пошёл вниз. */
static void test_один_проход_не_повторение(void)
{
    reset_all();
    CHECK(!pass_reed(), "один проход дал повторение");

    CHECK(load_reps() == 0, "один проход засчитан повторением: %u", load_reps());
    CHECK(load_passes() == 1, "проход не учтён: %lu", (unsigned long)load_passes());
}

/* Два прохода — одно повторение. Это и есть правило механики. */
static void test_два_прохода_одно_повторение(void)
{
    reset_all();

    CHECK(full_rep(), "повторение не засчитано");
    CHECK(load_reps() == 1, "повторений: %u, ожидалось 1", load_reps());
    CHECK(load_passes() == 2, "проходов: %lu", (unsigned long)load_passes());
}

/* Десять циклов — десять повторений, без накопления ошибки. */
static void test_счёт_не_плывёт(void)
{
    reset_all();

    for (int i = 0; i < 10; i++) {
        fake_advance(LOAD_MIN_REP_MS);
        full_rep();
    }

    CHECK(load_reps() == 10, "повторений: %u, ожидалось 10", load_reps());
    CHECK(load_passes() == 20, "проходов: %lu, ожидалось 20",
          (unsigned long)load_passes());
}

/* Дребезг контакта не превращается в повторения. */
static void test_дребезг_не_считается(void)
{
    reset_all();

    /* Звон: уровень скачет быстрее времени подтверждения. */
    for (int i = 0; i < 20; i++) {
        fake_input_level = (i % 2) ? 0xFFFF : 0;
        fake_advance(1);
        load_poll();
    }

    CHECK(load_reps() == 0, "дребезг дал повторений: %u", load_reps());
}

/*
 * Дрожание механики после удара не должно удваивать счёт.
 *
 * Два цикла подряд быстрее наименьшего промежутка — это отдача, а не
 * работа: человек столько не успевает.
 */
static void test_слишком_быстрые_повторения_отбрасываются(void)
{
    reset_all();

    fake_advance(LOAD_MIN_REP_MS);
    CHECK(full_rep(), "первое повторение не засчитано");

    /* Сразу следом, без паузы. */
    full_rep();

    CHECK(load_reps() == 1, "отдача механики засчитана: %u", load_reps());
    /* Проходы при этом учтены: наладке важно их видеть. */
    CHECK(load_passes() == 4, "проходы потеряны: %lu",
          (unsigned long)load_passes());
}

/* Счётчик накопительный: центр считает результат заезда приростом. */
static void test_счётчик_не_обнуляется(void)
{
    reset_all();

    for (int i = 0; i < 3; i++) {
        fake_advance(LOAD_MIN_REP_MS);
        full_rep();
    }
    uint16_t before = load_reps();

    fake_advance(60000); /* пауза между заходами */
    fake_advance(LOAD_MIN_REP_MS);
    full_rep();

    CHECK(load_reps() == before + 1,
          "счётчик сбросился между заходами: было %u, стало %u",
          before, load_reps());
}

int main(void)
{
    RUN(test_один_проход_не_повторение);
    RUN(test_два_прохода_одно_повторение);
    RUN(test_счёт_не_плывёт);
    RUN(test_дребезг_не_считается);
    RUN(test_слишком_быстрые_повторения_отбрасываются);
    RUN(test_счётчик_не_обнуляется);

    return tests_failed == 0 ? 0 : 1;
}
