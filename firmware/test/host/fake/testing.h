/* Минимальный набор для хостовых тестов: сборка без внешних зависимостей. */
#ifndef TESTING_H
#define TESTING_H

#include <stdbool.h>
#include <stdint.h>
#include <stdio.h>

extern int tests_failed;

#define CHECK(cond, ...)                                   \
    do {                                                   \
        if (!(cond)) {                                     \
            printf("  ПРОВАЛ: ");                          \
            printf(__VA_ARGS__);                           \
            printf("\n    (%s:%d)\n", __FILE__, __LINE__); \
            tests_failed++;                                \
        }                                                  \
    } while (0)

#define RUN(test)                 \
    do {                          \
        printf("== %s\n", #test); \
        test();                   \
    } while (0)

/* Управляемое время. */
void fake_advance(uint32_t ms);
void fake_set_time(uint32_t ms);

/* Уровни на выводах магнитов. */
extern int fake_pin_level[16];

/* Заглушка датчика колеса. */
int fake_trip_resets(void);
void fake_reset_hall(void);

#endif /* TESTING_H */
