#include <string.h>

#include "code.h"
#include "keypad.h"
#include "systime.h"

/*
 * Эталон хранится хешем, а не самим кодом.
 *
 * Хеш здесь не криптографический — на Cortex-M7 ради четырёх цифр
 * тащить SHA-256 незачем. Смысл скромнее: в дампе памяти или прошивки
 * не окажется кода в читаемом виде, и подсмотреть его через отладчик
 * не получится так же легко.
 */
static uint32_t code_hash;

static char entry[CODE_MAX_LEN + 1];
static uint8_t entry_len;
static uint32_t last_key_ms;

static uint8_t failures;
static uint32_t locked_until_ms;

/* FNV-1a: короткая, без таблиц и без зависимостей. */
static uint32_t hash_of(const char *s, uint8_t len)
{
    uint32_t h = 2166136261U;

    for (uint8_t i = 0; i < len; i++) {
        h ^= (uint32_t)(unsigned char)s[i];
        h *= 16777619U;
    }
    return h;
}

static void clear_entry(void)
{
    /* Затираем буфер, а не только длину: набранный код не должен
     * оставаться в памяти дольше, чем нужно. */
    memset(entry, 0, sizeof(entry));
    entry_len = 0;
}

void code_init(void)
{
    code_hash = hash_of(CODE_DEFAULT, (uint8_t)strlen(CODE_DEFAULT));
    clear_entry();
    failures = 0;
    locked_until_ms = 0;
    last_key_ms = millis();
}

bool code_set(const char *code)
{
    if (code == NULL) {
        return false;
    }

    size_t len = strlen(code);
    if (len < 3 || len > CODE_MAX_LEN) {
        return false;
    }
    for (size_t i = 0; i < len; i++) {
        if (code[i] < '0' || code[i] > '9') {
            return false;
        }
    }

    code_hash = hash_of(code, (uint8_t)len);
    clear_entry();
    failures = 0;
    locked_until_ms = 0;
    return true;
}

bool code_is_locked(void)
{
    if (locked_until_ms == 0U) {
        return false;
    }
    /* Знаковая разность переживает переполнение счётчика мс. */
    return (int32_t)(millis() - locked_until_ms) < 0;
}

uint8_t code_entered(void)
{
    return entry_len;
}

void code_poll(void)
{
    /* Недобранный код стирается сам: иначе цифры, набранные кем-то
     * раньше, допишутся к новому вводу. */
    if (entry_len > 0U &&
        (int32_t)(millis() - last_key_ms - CODE_ENTRY_TIMEOUT_MS) >= 0) {
        clear_entry();
    }
}

enum code_result code_feed(char key)
{
    if (key == KEYPAD_NO_KEY) {
        return CODE_PENDING;
    }

    last_key_ms = millis();

    if (key == CODE_KEY_CLEAR) {
        clear_entry();
        return CODE_CLEARED;
    }

    if (key != CODE_KEY_ENTER) {
        /* Всё, кроме цифр, игнорируется: буквенные клавиши на этой
         * матрице к коду отношения не имеют. */
        if (key < '0' || key > '9') {
            return CODE_PENDING;
        }
        if (entry_len < CODE_MAX_LEN) {
            entry[entry_len++] = key;
        }
        /* Переполнение буфера не ошибка ввода: лишние цифры просто
         * не попадают в набор, и код окажется неверным. */
        return CODE_PENDING;
    }

    /* Клавиша подтверждения — проверка набранного. */
    if (code_is_locked()) {
        clear_entry();
        return CODE_LOCKED;
    }
    if (entry_len == 0U) {
        return CODE_PENDING; /* нечего проверять */
    }

    uint32_t got = hash_of(entry, entry_len);
    clear_entry();

    if (got == code_hash) {
        failures = 0;
        return CODE_VALID;
    }

    if (++failures >= CODE_MAX_ATTEMPTS) {
        failures = 0;
        locked_until_ms = millis() + CODE_LOCKOUT_MS;
        return CODE_LOCKED;
    }
    return CODE_INVALID;
}

const char *code_result_name(enum code_result result)
{
    switch (result) {
    case CODE_PENDING:
        return "pending";
    case CODE_VALID:
        return "valid";
    case CODE_INVALID:
        return "invalid";
    case CODE_LOCKED:
        return "locked";
    case CODE_CLEARED:
        return "cleared";
    }
    return "unknown";
}
