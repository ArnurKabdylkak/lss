/*
 * Общая часть прошивки: тактирование, консоль и запуск роли.
 *
 * Плат на объекте несколько, заняты они разным — стойка набора чисел,
 * имитатор очага, — а тактирование, сеть и командный тракт у них
 * одни. Роль выбирается сборкой: в прошивку попадает ровно один
 * главный цикл, и ошибиться ролью в рабочем режиме невозможно.
 */
#include <libopencm3/cm3/nvic.h>
#include <libopencm3/cm3/systick.h>
#include <libopencm3/stm32/gpio.h>
#include <libopencm3/stm32/rcc.h>
#include <libopencm3/stm32/usart.h>

#include "discover.h"
#include "role.h"
#include "systime.h"

/* USART3 (ST-Link VCP): PD8 = TX, PD9 = RX */
#define USART_CONSOLE USART3

static volatile uint32_t system_millis;

void sys_tick_handler(void)
{
    system_millis++;
}

/* Не static: используется lwIP через sys_now() в net.c. */
uint32_t millis(void)
{
    return system_millis;
}

static void clock_setup(void)
{
    /* HSE 8 МГц (MCO от ST-Link) -> SYSCLK 216 МГц */
    rcc_clock_setup_hse(&rcc_3v3[RCC_CLOCK_3V3_216MHZ], 8);

    rcc_periph_clock_enable(RCC_GPIOB);
    rcc_periph_clock_enable(RCC_GPIOD);
    rcc_periph_clock_enable(RCC_USART3);
}

static void systick_setup(void)
{
    systick_set_clocksource(STK_CSR_CLKSOURCE_AHB);
    systick_set_reload(rcc_ahb_frequency / 1000U - 1U);
    systick_clear();
    systick_counter_enable();
    systick_interrupt_enable();
}

static void usart_setup(void)
{
    gpio_mode_setup(GPIOD, GPIO_MODE_AF, GPIO_PUPD_NONE, GPIO8 | GPIO9);
    gpio_set_af(GPIOD, GPIO_AF7, GPIO8 | GPIO9);

    usart_set_baudrate(USART_CONSOLE, 115200);
    usart_set_databits(USART_CONSOLE, 8);
    usart_set_stopbits(USART_CONSOLE, USART_STOPBITS_1);
    usart_set_parity(USART_CONSOLE, USART_PARITY_NONE);
    usart_set_flow_control(USART_CONSOLE, USART_FLOWCONTROL_NONE);
    usart_set_mode(USART_CONSOLE, USART_MODE_TX_RX);
    usart_enable(USART_CONSOLE);
}

void role_puts(const char *s)
{
    while (*s) {
        usart_send_blocking(USART_CONSOLE, (uint16_t)*s++);
    }
}

void role_put_u32(uint32_t value)
{
    char buf[11];
    int i = 0;

    do {
        buf[i++] = (char)('0' + (value % 10U));
        value /= 10U;
    } while (value);

    while (i--) {
        usart_send_blocking(USART_CONSOLE, (uint16_t)buf[i]);
    }
}

#if DISCOVER_MODE
/* Отчёт поиска разводки: печатается в консоль платы. */
static void discover_report(const char *a, const char *b, bool stuck)
{
    role_puts(stuck ? "ЗАЛИПЛО  " : "замкнуто ");
    role_puts(a);
    role_puts("  +  ");
    role_puts(b);
    role_puts("\r\n");
}
#endif

int main(void)
{
    clock_setup();
    systick_setup();
    usart_setup();

#if DISCOVER_MODE
    /* Режим поиска разводки: ни сети, ни роли — только ноги разъёма.
     * Удерживайте клавиши по одной, плата скажет, что с чем замкнуто. */
    discover_init();
    role_puts("Поиск разводки клавиатуры.\r\n");
    role_puts("Удерживайте клавиши по одной — покажу, какие ноги замкнуты.\r\n");
    while (1) {
        discover_scan(discover_report);
    }
#else
    role_run();
#endif

    return 0;
}
