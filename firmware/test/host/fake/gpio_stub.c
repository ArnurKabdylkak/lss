/* Заглушка GPIO: запоминает уровни выводов и отдаёт заданные входы. */
#include <libopencm3/stm32/gpio.h>

int fake_pin_level[16];

/* Входы: единицы — разомкнуто (подтяжка вверх), как у живой платы. */
uint16_t fake_input_level = 0xFFFF;

void gpio_mode_setup(uint32_t port, uint8_t mode, uint8_t pupd, uint16_t pins)
{
    (void)port;
    (void)mode;
    (void)pupd;
    (void)pins;
}

void gpio_set_output_options(uint32_t port, uint8_t otype, uint8_t speed,
                             uint16_t pins)
{
    (void)port;
    (void)otype;
    (void)speed;
    (void)pins;
}

void gpio_set(uint32_t port, uint16_t pins)
{
    (void)port;
    for (int i = 0; i < 16; i++) {
        if (pins & (1U << i)) {
            fake_pin_level[i] = 1;
        }
    }
}

void gpio_clear(uint32_t port, uint16_t pins)
{
    (void)port;
    for (int i = 0; i < 16; i++) {
        if (pins & (1U << i)) {
            fake_pin_level[i] = 0;
        }
    }
}

uint16_t gpio_get(uint32_t port, uint16_t pins)
{
    (void)port;
    return (uint16_t)(fake_input_level & pins);
}
