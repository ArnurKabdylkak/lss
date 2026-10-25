/*
 * Роль платы: стойка набора чисел.
 *
 * Плата отвечает на один вопрос: набранный код верен или нет. Ничего
 * не разблокирует и ничем не управляет — что делать с ответом,
 * решает центр (сценарий дымокамеры §4).
 */
#include <stdio.h>

#include <libopencm3/stm32/gpio.h>
#include <libopencm3/stm32/rcc.h>

#include "code.h"
#include "command.h"
#include "keypad.h"
#include "net.h"
#include "role.h"
#include "systime.h"

/*
 * Зелёный светодиод «код принят» — внешний, на стойке.
 *
 * Отдельная нога, а не светодиод платы: человек стоит у стойки в
 * задымлённом зале и смотрит на неё, а не на контроллер в щите.
 *
 * D10 на разъёме Arduino: свободен, рядом с клавиатурой и не занят ни
 * её строками (D5, D3, D4, D2), ни столбцами (D6, D7, D8, D9), ни
 * линиями Ethernet.
 *
 * Схема: нога -> резистор 330 Ом -> анод, катод на землю.
 */
#define LED_ACCESS_PORT GPIOD
#define LED_ACCESS_PIN  GPIO14 /* D10 */

/* Nucleo-F767ZI: LD1 = PB0 (зелёный) — горит, пока идёт набор. */
#define LED_ENTRY_PORT GPIOB
#define LED_ENTRY_PIN  GPIO0
#define LED_NET_PORT   GPIOB
#define LED_NET_PIN    GPIO7

/*
 * Сколько горит зелёный после верного набора.
 *
 * Не мигает и не гаснет сразу: человек, нажавший подтверждение,
 * поднимает глаза не мгновенно, а задание засчитывается один раз.
 */
#define ACCESS_HOLD_MS 5000U

static char payload[160];

/* До какого момента горит зелёный. Ноль — погашен. */
static uint32_t access_until_ms;

static void leds_setup(void)
{
    rcc_periph_clock_enable(RCC_GPIOB);
    rcc_periph_clock_enable(RCC_GPIOD);

    gpio_mode_setup(LED_ENTRY_PORT, GPIO_MODE_OUTPUT, GPIO_PUPD_NONE,
                    LED_ENTRY_PIN | LED_NET_PIN);
    gpio_mode_setup(LED_ACCESS_PORT, GPIO_MODE_OUTPUT, GPIO_PUPD_NONE,
                    LED_ACCESS_PIN);
    gpio_set_output_options(LED_ACCESS_PORT, GPIO_OTYPE_PP, GPIO_OSPEED_2MHZ,
                            LED_ACCESS_PIN);
    gpio_clear(LED_ACCESS_PORT, LED_ACCESS_PIN);
}

/*
 * Сообщить результат проверки кода.
 *
 * Сам код никуда не передаётся и не печатается — только ответ
 * «верно/неверно»: вводят его на виду у людей, и ни протокол, ни
 * консоль не место для набранных цифр.
 */
static void report_code(enum code_result result)
{
    const char *name = code_result_name(result);

    role_puts("code=");
    role_puts(name);
    role_puts("\r\n");

    int len = snprintf(payload, sizeof(payload),
                       "{\"event\":\"code\",\"code_result\":\"%s\","
                       "\"uptime_ms\":%lu}",
                       name, (unsigned long)millis());
    if (len > 0 && len < (int)sizeof(payload)) {
        net_send_telemetry(payload, (uint16_t)len);
    }
}

/*
 * Очередной отчёт о себе.
 *
 * Измерять стойке нечего, но молчать нельзя: сборщик считает
 * замолчавшее устройство пропавшим, а по падению uptime_ms видно, что
 * плата перезапустилась.
 */
static void report_alive(void)
{
    int len = snprintf(payload, sizeof(payload),
                       "{\"uptime_ms\":%lu}", (unsigned long)millis());
    if (len > 0 && len < (int)sizeof(payload)) {
        net_send_telemetry(payload, (uint16_t)len);
    }
}

void role_run(void)
{
    leds_setup();
    keypad_init();
    code_init();
    net_init();

    role_puts("Стойка набора чисел (STM32F767)\r\n");
    role_puts("Local " NET_LOCAL_IP " -> server " NET_SERVER_IP "\r\n");

    uint32_t next_report = millis();

    while (1) {
        /* Стек обслуживается на каждой итерации: приём кадров и таймеры. */
        net_poll();

        /* У клавиатуры свой интервал сканирования внутри. */
        keypad_poll();

        /* Недобранный код стирается по тайм-ауту. */
        code_poll();

        /* Команды разбираются здесь, а не в callback стека: работа в
         * контексте приёма кадра сломала бы модель суперцикла. */
        struct command cmd;
        while (command_pop(&cmd)) {
            struct command_ack ack;

            command_apply(&cmd, &ack);

            int len = command_format_ack(&ack, payload, (int)sizeof(payload));
            if (len > 0 && len < (int)sizeof(payload)) {
                net_send_ack(payload, (uint16_t)len);
            }

            role_puts(ack.accepted ? "cmd ok " : "cmd rejected ");
            role_puts(ack.reason);
            role_puts("\r\n");
        }

        /* За одну итерацию разбираем всю накопившуюся очередь нажатий. */
        for (char key = keypad_get_key(); key != KEYPAD_NO_KEY;
             key = keypad_get_key()) {
            /* Нажатие уходит в центр отдельным событием: по нему видно,
             * что стойкой пользуются, даже когда код ещё не добран. */
            int len = snprintf(payload, sizeof(payload),
                               "{\"event\":\"key\",\"key\":\"%c\","
                               "\"uptime_ms\":%lu}",
                               key, (unsigned long)millis());
            if (len > 0 && len < (int)sizeof(payload)) {
                net_send_telemetry(payload, (uint16_t)len);
            }

            enum code_result result = code_feed(key);
            if (result != CODE_PENDING) {
                report_code(result);
            }
            if (result == CODE_VALID) {
                /* Зелёный зажигается на самой стойке, не дожидаясь
                 * центра: человеку нужен ответ здесь и сейчас, а
                 * связь может моргнуть. */
                gpio_set(LED_ACCESS_PORT, LED_ACCESS_PIN);
                access_until_ms = millis() + ACCESS_HOLD_MS;
            }
        }

        /* Гасим зелёный по выдержке. */
        if (access_until_ms != 0U && (int32_t)(millis() - access_until_ms) >= 0) {
            access_until_ms = 0U;
            gpio_clear(LED_ACCESS_PORT, LED_ACCESS_PIN);
        }

        if ((int32_t)(millis() - next_report) >= 0) {
            next_report += command_period_ms();
            report_alive();

            role_puts(net_is_up() ? "link=up\r\n" : "link=down\r\n");

            if (code_entered() > 0) {
                gpio_set(LED_ENTRY_PORT, LED_ENTRY_PIN);
            } else {
                gpio_clear(LED_ENTRY_PORT, LED_ENTRY_PIN);
            }
            if (net_is_up()) {
                gpio_set(LED_NET_PORT, LED_NET_PIN);
            } else {
                gpio_clear(LED_NET_PORT, LED_NET_PIN);
            }
        }
    }
}
