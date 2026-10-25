/*
 * Поиск разводки клавиатуры.
 *
 * Клавиатуру на объекте подключили раньше, чем записали, какой вывод
 * куда идёт, а у матричной клавиатуры порядок строк и столбцов у
 * каждой модели свой. Гадать по описаниям бесполезно: даже у двух
 * клавиатур 4x4 выводы бывают разведены по-разному.
 *
 * Поэтому плата выясняет это сама. По очереди каждая нога становится
 * выходом и притягивается к земле, остальные читаются входами с
 * подтяжкой. Нажатая клавиша замыкает ровно две ноги — их и видно.
 *
 * Удерживая клавиши по одной, монтажник получает готовую таблицу: у
 * клавиш одного ряда общей окажется нога строки, у клавиш одного
 * столбца — нога столбца.
 *
 * Отдельно сообщается пара, замкнутая без всякого нажатия: так
 * находится залипший контакт или провод, посаженный на землю.
 */
#include <stdbool.h>
#include <stdint.h>
#include <stdio.h>

#include <libopencm3/stm32/gpio.h>
#include <libopencm3/stm32/rcc.h>

#include "discover.h"
#include "systime.h"

/*
 * Ноги разъёма Arduino на Nucleo-F767ZI — именно в них втыкают
 * шлейф клавиатуры.
 *
 * D11 (PA7) пропущен: он занят линией RMII_CRS_DV, и трогать его
 * значит уронить сеть, по которой плата отчитывается.
 */
static const struct {
    uint32_t port;
    uint16_t pin;
    const char *name;
} candidates[] = {
    {GPIOG, GPIO9,  "D0  PG9"},
    {GPIOG, GPIO14, "D1  PG14"},
    {GPIOF, GPIO15, "D2  PF15"},
    {GPIOE, GPIO13, "D3  PE13"},
    {GPIOF, GPIO14, "D4  PF14"},
    {GPIOE, GPIO11, "D5  PE11"},
    {GPIOE, GPIO9,  "D6  PE9"},
    {GPIOF, GPIO13, "D7  PF13"},
    {GPIOF, GPIO12, "D8  PF12"},
    {GPIOD, GPIO15, "D9  PD15"},
    {GPIOD, GPIO14, "D10 PD14"},
    {GPIOA, GPIO6,  "D12 PA6"},
    {GPIOA, GPIO5,  "D13 PA5"},
    {GPIOB, GPIO9,  "D14 PB9"},
    {GPIOB, GPIO8,  "D15 PB8"},
};

#define CANDIDATES ((int)(sizeof(candidates) / sizeof(candidates[0])))

/* Сколько проходов подряд пара должна держаться, чтобы её приняли.
 * Контакт звенит, а наводка приходит пачками: одиночное замыкание
 * в таблицу не попадёт. */
#define HOLD_SCANS 3

static uint8_t seen[CANDIDATES][CANDIDATES];
static bool reported[CANDIDATES][CANDIDATES];

static void all_inputs(void)
{
    for (int i = 0; i < CANDIDATES; i++) {
        gpio_mode_setup(candidates[i].port, GPIO_MODE_INPUT, GPIO_PUPD_PULLUP,
                        candidates[i].pin);
    }
}

static void drive_low(int i)
{
    gpio_mode_setup(candidates[i].port, GPIO_MODE_OUTPUT, GPIO_PUPD_NONE,
                    candidates[i].pin);
    gpio_set_output_options(candidates[i].port, GPIO_OTYPE_OD,
                            GPIO_OSPEED_2MHZ, candidates[i].pin);
    gpio_clear(candidates[i].port, candidates[i].pin);
}

static void settle(void)
{
    /* Ёмкость шлейфа и подтяжка дают фронт в единицы микросекунд. */
    for (volatile int d = 0; d < 400; d++) {
        __asm__("nop");
    }
}

void discover_init(void)
{
    rcc_periph_clock_enable(RCC_GPIOA);
    rcc_periph_clock_enable(RCC_GPIOB);
    rcc_periph_clock_enable(RCC_GPIOD);
    rcc_periph_clock_enable(RCC_GPIOE);
    rcc_periph_clock_enable(RCC_GPIOF);
    rcc_periph_clock_enable(RCC_GPIOG);

    all_inputs();

    for (int i = 0; i < CANDIDATES; i++) {
        for (int j = 0; j < CANDIDATES; j++) {
            seen[i][j] = 0;
            reported[i][j] = false;
        }
    }
}

/*
 * Один проход по всем ногам. Найденные пары отдаются вызывающему
 * через report: печать в консоль — не дело поиска.
 */
void discover_scan(void (*report)(const char *a, const char *b, bool stuck))
{
    for (int i = 0; i < CANDIDATES; i++) {
        all_inputs();
        drive_low(i);
        settle();

        for (int j = 0; j < CANDIDATES; j++) {
            if (j == i) {
                continue;
            }
            bool shorted = !gpio_get(candidates[j].port, candidates[j].pin);

            if (!shorted) {
                seen[i][j] = 0;
                /* Пара разомкнулась — о следующем замыкании сообщим
                 * заново: так видно каждое нажатие, а не только первое. */
                reported[i][j] = false;
                continue;
            }

            if (seen[i][j] < 255) {
                seen[i][j]++;
            }
            if (seen[i][j] == HOLD_SCANS && !reported[i][j]) {
                reported[i][j] = true;
                /* Залипание отличается по времени: пара, замкнутая с
                 * первых секунд работы, нажатием быть не может —
                 * человек не успел бы. */
                report(candidates[i].name, candidates[j].name, millis() < 3000U);
            }
        }
    }

    all_inputs();
}
