#include <libopencm3/stm32/gpio.h>
#include <libopencm3/stm32/rcc.h>

#include "load.h"
#include "systime.h"

/*
 * Счётчик повторений ударного молота.
 *
 * Опрос, а не прерывание: частота событий — единицы в секунду, а
 * прерывание от дребезжащего геркона пришлось бы гасить тем же
 * способом, только в контексте, где этого делать нельзя.
 */

static uint16_t reps;
static uint32_t passes;

/* Принятый уровень геркона и время последней смены. */
static bool reed_raw = true;      /* подтяжка держит единицу */
static bool reed_accepted = true; /* контакт разомкнут */
static uint32_t reed_since_ms;

/* Когда засчитали прошлое повторение: защита от дребезга механики. */
static uint32_t last_rep_ms;

/* Проходов накоплено внутри текущего повторения. */
static uint8_t partial;

void load_init(void)
{
    rcc_periph_clock_enable(RCC_GPIOF);

    gpio_mode_setup(LOAD_REED_PORT, GPIO_MODE_INPUT, GPIO_PUPD_PULLUP,
                    LOAD_REED_PIN);

    reps = 0;
    passes = 0;
    partial = 0;
    reed_raw = true;
    reed_accepted = true;
    reed_since_ms = millis();
    last_rep_ms = 0;
}

uint16_t load_reps(void)
{
    return reps;
}

uint32_t load_passes(void)
{
    return passes;
}

bool load_poll(void)
{
    uint32_t now = millis();

    bool level = gpio_get(LOAD_REED_PORT, LOAD_REED_PIN) != 0;
    if (level != reed_raw) {
        reed_raw = level;
        reed_since_ms = now;
        return false;
    }
    if (reed_accepted == reed_raw ||
        (now - reed_since_ms) < LOAD_DEBOUNCE_MS) {
        return false;
    }
    reed_accepted = reed_raw;

    /* Проход засчитывается по замыканию: магнит подошёл к геркону.
     * Размыкание — это уход магнита, и считать его вторым событием
     * нельзя, иначе один проход дал бы два. */
    if (reed_accepted) {
        return false;
    }

    passes++;
    partial++;
    if (partial < LOAD_PASSES_PER_REP) {
        return false;
    }
    partial = 0;

    /* Слишком частые повторения — это дрожание механики после удара,
     * а не работа. Проход при этом уже учтён: наладке важно видеть,
     * что геркон срабатывал. */
    if (last_rep_ms != 0U && (now - last_rep_ms) < LOAD_MIN_REP_MS) {
        return false;
    }
    last_rep_ms = now;

    if (reps < UINT16_MAX) {
        reps++;
    }
    return true;
}
