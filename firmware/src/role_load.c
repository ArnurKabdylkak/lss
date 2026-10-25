/*
 * Роль платы: ударный молот.
 *
 * Считает повторения герконом и отчитывается центру. Магнит проходит
 * мимо геркона дважды за цикл, поэтому повторением считается каждое
 * второе срабатывание — иначе зачёт оказался бы вдвое щедрее работы.
 *
 * Зачёт ведётся приростом: счётчик на плате накопительный и между
 * заходами не обнуляется. Центр запоминает показания при назначении
 * человека на тренажёр и вычитает их в конце — так перезапуск платы
 * посреди смены виден как падение счётчика, а не как выполненная
 * норма.
 */
#include <stdio.h>

#include <libopencm3/stm32/gpio.h>
#include <libopencm3/stm32/rcc.h>

#include "command.h"
#include "load.h"
#include "net.h"
#include "role.h"
#include "systime.h"

/* Nucleo-F767ZI: LD1 = PB0 (зелёный) — мигает на каждом повторении. */
#define LED_REP_PORT GPIOB
#define LED_REP_PIN  GPIO0
#define LED_NET_PORT GPIOB
#define LED_NET_PIN  GPIO7

/* Сколько горит светодиод после засчитанного повторения, мс. */
#define REP_BLINK_MS 150U

static char payload[192];
static uint32_t blink_until_ms;

/* Сообщить о засчитанном повторении.
 *
 * Отдельным событием, а не только в очередном отчёте: события центр
 * хранит вечно, а показания стареют. По ним потом видно, в каком
 * темпе шла работа, а не только итог. */
static void report_rep(void)
{
    role_puts("повторение\r\n");

    int len = snprintf(payload, sizeof(payload),
                       "{\"event\":\"set\",\"sets\":%u,\"load_kg\":%u,"
                       "\"uptime_ms\":%lu}",
                       (unsigned)load_reps(), (unsigned)LOAD_WEIGHT_KG,
                       (unsigned long)millis());
    if (len > 0 && len < (int)sizeof(payload)) {
        net_send_telemetry(payload, (uint16_t)len);
    }
}

/* Очередной отчёт: накопленные повторения и вес груза. */
static void report_alive(void)
{
    int len = snprintf(payload, sizeof(payload),
                       "{\"sets\":%u,\"load_kg\":%u,\"uptime_ms\":%lu}",
                       (unsigned)load_reps(), (unsigned)LOAD_WEIGHT_KG,
                       (unsigned long)millis());
    if (len > 0 && len < (int)sizeof(payload)) {
        net_send_telemetry(payload, (uint16_t)len);
    }
}

void role_run(void)
{
    rcc_periph_clock_enable(RCC_GPIOB);
    gpio_mode_setup(LED_REP_PORT, GPIO_MODE_OUTPUT, GPIO_PUPD_NONE,
                    LED_REP_PIN | LED_NET_PIN);

    load_init();
    net_init();

    role_puts("Ударный молот (STM32F767)\r\n");
    role_puts("Local " NET_LOCAL_IP " -> server " NET_SERVER_IP "\r\n");

    uint32_t next_report = millis();

    while (1) {
        net_poll();

        if (load_poll()) {
            report_rep();
            gpio_set(LED_REP_PORT, LED_REP_PIN);
            blink_until_ms = millis() + REP_BLINK_MS;
        }

        if (blink_until_ms != 0U &&
            (int32_t)(millis() - blink_until_ms) >= 0) {
            blink_until_ms = 0U;
            gpio_clear(LED_REP_PORT, LED_REP_PIN);
        }

        struct command cmd;
        while (command_pop(&cmd)) {
            struct command_ack ack;

            command_apply(&cmd, &ack);

            int len = command_format_ack(&ack, payload, (int)sizeof(payload));
            if (len > 0 && len < (int)sizeof(payload)) {
                net_send_ack(payload, (uint16_t)len);
            }
        }

        if ((int32_t)(millis() - next_report) >= 0) {
            next_report += command_period_ms();
            report_alive();

            /* Проходы геркона — наладке: по ним видно разницу между
             * «механика не доходит до геркона» и «геркон не
             * срабатывает вовсе». */
            role_puts("повторений=");
            role_put_u32(load_reps());
            role_puts("  проходов=");
            role_put_u32(load_passes());
            role_puts(net_is_up() ? "  link=up\r\n" : "  link=down\r\n");

            if (net_is_up()) {
                gpio_set(LED_NET_PORT, LED_NET_PIN);
            } else {
                gpio_clear(LED_NET_PORT, LED_NET_PIN);
            }
        }
    }
}
