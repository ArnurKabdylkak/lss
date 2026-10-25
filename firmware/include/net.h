/*
 * Сетевой уровень: lwIP (NO_SYS=1) со статическим IP и UDP-телеметрией.
 */
#ifndef NET_H
#define NET_H

#include <stdbool.h>
#include <stdint.h>

/*
 * Адресация устройства (статический IP).
 *
 * Значения перекрываются из platformio.ini: плат на объекте несколько,
 * они стоят в разных помещениях и отчитываются разным сборщикам, а
 * прошивка у них одна. Править заголовок под каждую плату значило бы
 * держать столько же веток исходника.
 *
 * Умолчания ниже — стенд разработчика, а не объект.
 */
#ifndef NET_LOCAL_IP
#define NET_LOCAL_IP    "192.168.1.50"
#endif
#ifndef NET_NETMASK
#define NET_NETMASK     "255.255.255.0"
#endif
#ifndef NET_GATEWAY
#define NET_GATEWAY     "192.168.1.1"
#endif

/* --- Куда отправляется телеметрия --- */
#ifndef NET_SERVER_IP
#define NET_SERVER_IP   "192.168.1.10"
#endif
#ifndef NET_SERVER_PORT
#define NET_SERVER_PORT 5005
#endif
#ifndef NET_LOCAL_PORT
#define NET_LOCAL_PORT  5006
#endif

/* Инициализация lwIP и интерфейса. Вызывать после настройки тактирования
 * и SysTick (lwIP использует sys_now()). */
void net_init(void);

/* Обслуживание стека: приём кадров, таймеры lwIP, опрос линка.
 * Вызывать в главном цикле как можно чаще. */
void net_poll(void);

/* Отправить датаграмму на сервер. true — пакет передан в стек. */
bool net_send_telemetry(const char *payload, uint16_t len);

/*
 * Отправить подтверждение команды тому, кто её прислал.
 *
 * Адрес берётся не из конфигурации, а из принятой команды: сборщик
 * может стоять не там, куда уходит телеметрия, и отвечать надо ему.
 */
bool net_send_ack(const char *payload, uint16_t len);

/* Поднят ли линк и настроен ли интерфейс. */
bool net_is_up(void);

#endif /* NET_H */
