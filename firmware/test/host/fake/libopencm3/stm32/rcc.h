#pragma once
#include <stdint.h>
#define RCC_GPIOF 5
#define RCC_GPIOG 6
#define RCC_GPIOE 4
#define RCC_GPIOD 3
static inline void rcc_periph_clock_enable(uint32_t p){(void)p;}
