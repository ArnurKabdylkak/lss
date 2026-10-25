/* Заглушка GPIO для хостовых тестов: запоминает уровень на выводах. */
#ifndef FAKE_GPIO_H
#define FAKE_GPIO_H

#include <stdint.h>

#define GPIOA 0
#define GPIOB 1
#define GPIOC 2
#define GPIOD 3
#define GPIOE 4
#define GPIOF 5
#define GPIOG 6

#define GPIO0  (1U << 0)
#define GPIO5  (1U << 5)
#define GPIO6  (1U << 6)
#define GPIO8  (1U << 8)
#define GPIO12 (1U << 12)
#define GPIO15 (1U << 15)
#define GPIO7  (1U << 7)
#define GPIO9  (1U << 9)
#define GPIO11 (1U << 11)
#define GPIO13 (1U << 13)
#define GPIO14 (1U << 14)

#define GPIO_MODE_OUTPUT 1
#define GPIO_MODE_INPUT  0
#define GPIO_PUPD_PULLUP 1
#define GPIO_OTYPE_OD    1
#define GPIO_PUPD_NONE   0
#define GPIO_OTYPE_PP    0
#define GPIO_OSPEED_2MHZ 0

/* Уровни на выводах порта E: индекс — номер вывода. Тесты смотрят сюда,
 * чтобы проверить, держит магнит препятствие или отпустил. */
extern int fake_pin_level[16];

void gpio_mode_setup(uint32_t port, uint8_t mode, uint8_t pupd, uint16_t pins);
void gpio_set_output_options(uint32_t port, uint8_t otype, uint8_t speed,
                             uint16_t pins);
void gpio_set(uint32_t port, uint16_t pins);
void gpio_clear(uint32_t port, uint16_t pins);
uint16_t gpio_get(uint32_t port, uint16_t pins);

/* Состояние входов: тест задаёт его руками, подменяя геркон. */
extern uint16_t fake_input_level;

#endif /* FAKE_GPIO_H */
