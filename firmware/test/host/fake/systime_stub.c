/* Управляемое время для хостовых тестов. */
#include <stdint.h>

static uint32_t fake_now;

void fake_advance(uint32_t ms) { fake_now += ms; }
void fake_set_time(uint32_t ms) { fake_now = ms; }

uint32_t millis(void) { return fake_now; }
