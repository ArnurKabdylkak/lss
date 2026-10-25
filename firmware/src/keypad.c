#include <libopencm3/stm32/gpio.h>
#include <libopencm3/stm32/rcc.h>

#include "keypad.h"
#include "systime.h"

/*
 * Раскладка клавиатуры.
 *
 * По умолчанию — обычная плёночная матрица: ею набирают код, и на ней
 * есть «решётка» и «звёздочка», которыми набор подтверждают и стирают.
 * Шестнадцатеричная раскладка (как в исходном скетче) включается
 * флагом KEYPAD_LAYOUT_HEX; там подтверждение и стирание назначены на
 * клавиши E и F — см. CODE_KEY_ENTER в code.h.
 */
#if KEYPAD_LAYOUT_HEX
static const char keymap[KEYPAD_ROWS][KEYPAD_COLS] = {
    {'0', '1', '2', '3'},
    {'4', '5', '6', '7'},
    {'8', '9', 'A', 'B'},
    {'C', 'D', 'E', 'F'},
};
#else
static const char keymap[KEYPAD_ROWS][KEYPAD_COLS] = {
    {'1', '2', '3', 'A'},
    {'4', '5', '6', 'B'},
    {'7', '8', '9', 'C'},
    {'*', '0', '#', 'D'},
};
#endif

/*
 * Пины в терминах Arduino-разъёма Nucleo-F767ZI:
 *   строки   D6=PE9,  D7=PF13, D8=PF12, D9=PD15
 *   столбцы  D5=PE11, D4=PF14, D3=PE13, D2=PF15
 * Ни один из них не конфликтует с занятыми в проекте:
 * PA0 (датчик), PD8/PD9 (USART3), PB0/PB7 (светодиоды)
 * и линиями RMII (PA1/PA2/PA7/PB13/PC1/PC4/PC5/PG11/PG13).
 */
struct pin {
    uint32_t port;
    uint16_t pin;
};

/*
 * Разводка снята с объекта режимом поиска (src/discover.c): клавишу
 * удерживали, плата показывала, какая пара ног замкнулась.
 *
 *            PE9    PF13   PF12   PD15     <- столбцы
 *   PE11      1      2      3      A
 *   PE13      4      5      6      B
 *   PF14      7      8      9      C
 *   PF15      *      0      #      D
 *      ^ строки
 *
 * Строки на D5, D3, D4, D2, столбцы на D6, D7, D8, D9 — половины
 * разъёма стоят зеркально тому, как шлейф кладут обычно. Так он
 * смонтирован, и раскладка подогнана под провода: менять проводку в
 * собранной стойке дороже и рискованнее, чем таблицу здесь.
 */
static const struct pin row_pins[KEYPAD_ROWS] = {
    {GPIOE, GPIO11},  /* D5 */
    {GPIOE, GPIO13},  /* D3 */
    {GPIOF, GPIO14},  /* D4 */
    {GPIOF, GPIO15},  /* D2 */
};

static const struct pin col_pins[KEYPAD_COLS] = {
    {GPIOE, GPIO9},   /* D6 */
    {GPIOF, GPIO13},  /* D7 */
    {GPIOF, GPIO12},  /* D8 */
    {GPIOD, GPIO15},  /* D9 */
};

/* Подтверждённое и «сырое» состояние матрицы, бит на клавишу. */
static uint16_t stable_state;
static uint16_t raw_state;
static uint32_t raw_changed_ms;
static uint32_t next_scan_ms;

/* Кольцевая очередь нажатий: обработчик в главном цикле может
 * не успевать забирать их по одному за итерацию. */
#define KEY_QUEUE_LEN 8
static char key_queue[KEY_QUEUE_LEN];
static uint8_t queue_head;
static uint8_t queue_tail;

static void queue_push(char key)
{
    uint8_t next = (uint8_t)((queue_head + 1U) % KEY_QUEUE_LEN);

    if (next == queue_tail) {
        /* Очередь переполнена — теряем самое старое нажатие. */
        queue_tail = (uint8_t)((queue_tail + 1U) % KEY_QUEUE_LEN);
    }

    key_queue[queue_head] = key;
    queue_head = next;
}

void keypad_init(void)
{
    rcc_periph_clock_enable(RCC_GPIOD);
    rcc_periph_clock_enable(RCC_GPIOE);
    rcc_periph_clock_enable(RCC_GPIOF);

    /* Строки — выходы с открытым стоком: при сканировании активная строка
     * притягивается к земле, остальные остаются в высокоимпедансном
     * состоянии. Так одновременное нажатие двух клавиш в одном столбце
     * не приводит к короткому замыканию выходов друг на друга. */
    for (int r = 0; r < KEYPAD_ROWS; r++) {
        gpio_mode_setup(row_pins[r].port, GPIO_MODE_OUTPUT, GPIO_PUPD_NONE,
                        row_pins[r].pin);
        gpio_set_output_options(row_pins[r].port, GPIO_OTYPE_OD,
                                GPIO_OSPEED_2MHZ, row_pins[r].pin);
        gpio_set(row_pins[r].port, row_pins[r].pin);  /* отпущена */
    }

    /* Столбцы — входы с подтяжкой вверх: нажатая клавиша даёт логический 0. */
    for (int c = 0; c < KEYPAD_COLS; c++) {
        gpio_mode_setup(col_pins[c].port, GPIO_MODE_INPUT, GPIO_PUPD_PULLUP,
                        col_pins[c].pin);
    }

    stable_state = 0;
    raw_state = 0;
    raw_changed_ms = millis();
    next_scan_ms = millis();
    queue_head = 0;
    queue_tail = 0;
}

/* Один проход по матрице: возвращает битовую маску нажатых клавиш. */
static uint16_t keypad_scan(void)
{
    uint16_t state = 0;

    for (int r = 0; r < KEYPAD_ROWS; r++) {
        gpio_clear(row_pins[r].port, row_pins[r].pin);

        /* Пауза на установление уровня: ёмкость линии и подтяжка
         * дают фронт в единицы микросекунд. */
        for (volatile int d = 0; d < 200; d++) {
            __asm__("nop");
        }

        for (int c = 0; c < KEYPAD_COLS; c++) {
            if (!gpio_get(col_pins[c].port, col_pins[c].pin)) {
                state |= (uint16_t)(1U << (r * KEYPAD_COLS + c));
            }
        }

        gpio_set(row_pins[r].port, row_pins[r].pin);
    }

    return state;
}

void keypad_poll(void)
{
    uint32_t now = millis();

    if ((int32_t)(now - next_scan_ms) < 0) {
        return;
    }
    next_scan_ms = now + KEYPAD_SCAN_INTERVAL_MS;

    uint16_t state = keypad_scan();

    if (state != raw_state) {
        /* Картина изменилась — начинаем отсчёт времени подавления дребезга. */
        raw_state = state;
        raw_changed_ms = now;
        return;
    }

    if ((now - raw_changed_ms) < KEYPAD_DEBOUNCE_MS) {
        return;
    }

    if (state == stable_state) {
        return;
    }

    /* В очередь попадают только новые нажатия; отпускания не сообщаются,
     * как и в исходном getKey() на Arduino. */
    uint16_t pressed = (uint16_t)(state & ~stable_state);
    stable_state = state;

    for (int r = 0; r < KEYPAD_ROWS; r++) {
        for (int c = 0; c < KEYPAD_COLS; c++) {
            if (pressed & (uint16_t)(1U << (r * KEYPAD_COLS + c))) {
                queue_push(keymap[r][c]);
            }
        }
    }
}

char keypad_get_key(void)
{
    if (queue_tail == queue_head) {
        return KEYPAD_NO_KEY;
    }

    char key = key_queue[queue_tail];
    queue_tail = (uint8_t)((queue_tail + 1U) % KEY_QUEUE_LEN);

    return key;
}

bool keypad_is_pressed(void)
{
    return stable_state != 0;
}
