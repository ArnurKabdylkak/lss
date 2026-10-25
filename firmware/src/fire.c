#include <libopencm3/stm32/gpio.h>
#include <libopencm3/stm32/rcc.h>

#include "fire.h"
#include "systime.h"

/*
 * Состояние очага.
 *
 * Плата решает сама, когда загореться: связь с центром может моргнуть,
 * а занятие при этом идёт. Центр может вмешаться командой, но ждать
 * его разрешения очаг не обязан.
 */
static bool burning;
static const char *reason = "init";

/*
 * Подтверждение контакта кнопки.
 *
 * raw — то, что читается сейчас; accepted — принятый уровень. Между
 * ними выдержка: механика звенит, а каждое ложное срабатывание
 * засчитало бы задание маршрута.
 */
static bool button_raw = true;      /* подтяжка держит единицу */
static bool button_accepted = true; /* отпущена */
static uint32_t button_since_ms;

static void drive_relay(bool on)
{
    /* Прямое управление: единица включает нагрузку. */
    if (on) {
        gpio_set(FIRE_RELAY_PORT, FIRE_RELAY_PIN);
    } else {
        gpio_clear(FIRE_RELAY_PORT, FIRE_RELAY_PIN);
    }
}

void fire_init(void)
{
    rcc_periph_clock_enable(RCC_GPIOD);
    rcc_periph_clock_enable(RCC_GPIOF);

    gpio_mode_setup(FIRE_RELAY_PORT, GPIO_MODE_OUTPUT, GPIO_PUPD_NONE,
                    FIRE_RELAY_PIN);
    gpio_set_output_options(FIRE_RELAY_PORT, GPIO_OTYPE_PP, GPIO_OSPEED_2MHZ,
                            FIRE_RELAY_PIN);

    gpio_mode_setup(FIRE_BUTTON_PORT, GPIO_MODE_INPUT, GPIO_PUPD_PULLUP,
                    FIRE_BUTTON_PIN);

    /* Очаг при старте горит: плату включают перед занятием, и зал
     * должен быть готов без лишних действий. */
    burning = true;
    reason = "init";
    drive_relay(true);

    button_raw = true; /* подтяжка держит единицу, пока не нажато */
    button_accepted = true;
    button_since_ms = millis();
}

bool fire_is_burning(void)
{
    return burning;
}

const char *fire_last_reason(void)
{
    return reason;
}

void fire_set(bool on)
{
    if (burning == on) {
        return;
    }
    burning = on;
    reason = "command";
    drive_relay(on);
}

bool fire_poll(void)
{
    uint32_t now = millis();

    /* Кнопка тушения. Нажатие — это ноль на ноге. */
    bool level = gpio_get(FIRE_BUTTON_PORT, FIRE_BUTTON_PIN) != 0;
    if (level != button_raw) {
        button_raw = level;
        button_since_ms = now;
    } else if (button_accepted != button_raw &&
               (now - button_since_ms) >= FIRE_DEBOUNCE_MS) {
        button_accepted = button_raw;

        /* Тушит только горящий очаг: нажатие на потухший ничего не
         * значит и в центр не идёт. */
        if (!button_accepted && burning) {
            burning = false;
            reason = "button";
            drive_relay(false);
            return true;
        }
    }

    return false;
}
