/*
 * Роль платы: имитатор очага пламени.
 *
 * Очаг загорается сам в случайный момент и гаснет кнопкой на самом
 * очаге — тем, кто его нашёл. Центр узнаёт об обоих событиях и по
 * тушению засчитывает задание маршрута.
 *
 * Решение о зажигании принимает плата, а не центр: связь может
 * моргнуть, а занятие при этом идёт. Центр может вмешаться командой,
 * но ждать его разрешения очаг не обязан.
 */
#include <stdio.h>

#include <libopencm3/stm32/gpio.h>

#include "command.h"
#include "fire.h"
#include "net.h"
#include "role.h"
#include "systime.h"

static char payload[160];

/* Сообщить центру, что очаг загорелся или потушен. */
static void report_fire(void)
{
    const char *state = fire_is_burning() ? "on" : "off";

    role_puts("очаг=");
    role_puts(state);
    role_puts(" причина=");
    role_puts(fire_last_reason());
    role_puts("\r\n");

    /* Формат тот же, что у прочих реле объекта: центр засчитывает
     * задание по событию «реле выключено». Отдельного вида события
     * для очага не заводим — он ничем не отличается от остальных. */
    int len = snprintf(payload, sizeof(payload),
                       "{\"event\":\"relay\",\"state\":\"%s\",\"reason\":\"%s\","
                       "\"uptime_ms\":%lu}",
                       state, fire_last_reason(), (unsigned long)millis());
    if (len > 0 && len < (int)sizeof(payload)) {
        net_send_telemetry(payload, (uint16_t)len);
    }
}

/* Очередной отчёт о себе: состояние очага и время работы. */
static void report_alive(void)
{
    int len = snprintf(payload, sizeof(payload),
                       "{\"state\":\"%s\",\"uptime_ms\":%lu}",
                       fire_is_burning() ? "on" : "off",
                       (unsigned long)millis());
    if (len > 0 && len < (int)sizeof(payload)) {
        net_send_telemetry(payload, (uint16_t)len);
    }
}

void role_run(void)
{
    fire_init();
    net_init();

    role_puts("Имитатор очага (STM32F767)\r\n");
    role_puts("Local " NET_LOCAL_IP " -> server " NET_SERVER_IP "\r\n");

    uint32_t next_report = millis();

    while (1) {
        net_poll();

        /* Зажигание и тушение — здесь же, в общем цикле: работа в
         * обработчике сети сломала бы модель суперцикла. */
        if (fire_poll()) {
            report_fire();
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

            role_puts(fire_is_burning() ? "горит" : "потушен");
            role_puts(net_is_up() ? "  link=up\r\n" : "  link=down\r\n");

            /* Светодиод платы повторяет состояние очага: по нему
             * видно, что происходит, не подходя к зеву. */
            if (fire_is_burning()) {
                gpio_set(GPIOB, GPIO14); /* LD3, красный */
            } else {
                gpio_clear(GPIOB, GPIO14);
            }
        }
    }
}
