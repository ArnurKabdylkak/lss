/* Прослойка lwIP под arm-none-eabi-gcc / Cortex-M7. */
#ifndef LWIP_ARCH_CC_H
#define LWIP_ARCH_CC_H

#include <stdint.h>
#include <stddef.h>

/* Cortex-M7 — little endian. */
#define BYTE_ORDER LITTLE_ENDIAN

/* Форматы для отладочного вывода lwIP. */
#define U16_F "hu"
#define S16_F "hd"
#define X16_F "hx"
#define U32_F "lu"
#define S32_F "ld"
#define X32_F "lx"
#define SZT_F "u"

#define LWIP_NO_UNISTD_H 1

/* Выравнивание структур пакетов: GCC-синтаксис. */
#define PACK_STRUCT_BEGIN
#define PACK_STRUCT_STRUCT __attribute__((packed))
#define PACK_STRUCT_END
#define PACK_STRUCT_FIELD(x) x

void lwip_platform_assert(const char *msg, const char *file, int line);

#define LWIP_PLATFORM_DIAG(x)
#define LWIP_PLATFORM_ASSERT(x) lwip_platform_assert(x, __FILE__, __LINE__)

#endif /* LWIP_ARCH_CC_H */
