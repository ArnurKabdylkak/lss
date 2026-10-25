#include <string.h>

#include "lwip/init.h"
#include "lwip/ip_addr.h"
#include "lwip/netif.h"
#include "lwip/pbuf.h"
#include "lwip/timeouts.h"
#include "lwip/sys.h"
#include "lwip/udp.h"

#include "command.h"
#include "ethernetif.h"
#include "net.h"
#include "systime.h"

static struct netif eth_netif;
static struct udp_pcb *telemetry_pcb;
static ip4_addr_t server_addr;

/* Откуда пришла последняя команда: туда же уходит подтверждение. */
static ip_addr_t last_sender;
static u16_t last_sender_port;
static bool have_sender;

/* lwIP в режиме NO_SYS=1 берёт время отсюда. */
uint32_t sys_now(void)
{
    return millis();
}

/* Вызывается из LWIP_PLATFORM_ASSERT (arch/cc.h). */
void lwip_platform_assert(const char *msg, const char *file, int line)
{
    (void)msg;
    (void)file;
    (void)line;

    /* Нарушен инвариант стека — дальше работать нельзя. */
    while (1) {
        __asm__("bkpt 0");
    }
}

/*
 * Приём команд. Вызывается стеком lwIP в контексте обработки кадра,
 * поэтому здесь только разбор и постановка в очередь: исполнение —
 * в главном цикле, рядом с остальными задачами (ARCHITECTURE.md §7.3).
 * Делать работу здесь нельзя — это сломало бы модель суперцикла.
 */
static void on_command(void *arg, struct udp_pcb *pcb, struct pbuf *p,
                       const ip_addr_t *addr, u16_t port)
{
    (void)arg;
    (void)pcb;

    if (p == NULL) {
        return;
    }

    /* Датаграмма может быть собрана из нескольких pbuf: команда
     * короткая, поэтому просто копируем её в сплошной буфер. */
    char buf[COMMAND_MAX_LEN];
    u16_t len = pbuf_copy_partial(p, buf, sizeof(buf) - 1U, 0);
    pbuf_free(p);

    if (len == 0U) {
        return;
    }
    buf[len] = '\0';

    struct command cmd;
    if (!command_parse(buf, len, &cmd)) {
        return;
    }

    last_sender = *addr;
    last_sender_port = port;
    have_sender = true;

    command_push(&cmd);
}

void net_init(void)
{
    ip4_addr_t ipaddr, netmask, gateway;

    lwip_init();

    ip4addr_aton(NET_LOCAL_IP, &ipaddr);
    ip4addr_aton(NET_NETMASK, &netmask);
    ip4addr_aton(NET_GATEWAY, &gateway);
    ip4addr_aton(NET_SERVER_IP, &server_addr);

    netif_add(&eth_netif, &ipaddr, &netmask, &gateway, NULL,
              ethernetif_init, netif_input);
    netif_set_default(&eth_netif);
    netif_set_up(&eth_netif);

    telemetry_pcb = udp_new();
    if (telemetry_pcb != NULL) {
        /* Привязка к локальному порту нужна, чтобы сервер видел
         * предсказуемый source port и мог отвечать. */
        udp_bind(telemetry_pcb, IP_ADDR_ANY, NET_LOCAL_PORT);

        /* Обратный канал: тот же сокет принимает команды. Без этого
         * вызова входящие датаграммы молча отбрасываются стеком. */
        udp_recv(telemetry_pcb, on_command, NULL);
    }
}

void net_poll(void)
{
    ethernetif_poll(&eth_netif);

    /* Опрос PHY по MDIO медленный, чаще чем раз в 100 мс не нужен. */
    static uint32_t next_link_check;
    if ((int32_t)(millis() - next_link_check) >= 0) {
        next_link_check = millis() + 100U;
        ethernetif_check_link(&eth_netif);
    }

    sys_check_timeouts();
}

bool net_is_up(void)
{
    return netif_is_up(&eth_netif) && netif_is_link_up(&eth_netif);
}

/* Общая отправка: телеметрия идёт на сервер, подтверждения — отправителю. */
static bool net_send_to(const ip_addr_t *addr, u16_t port,
                        const char *payload, uint16_t len)
{
    if (telemetry_pcb == NULL || !net_is_up()) {
        return false;
    }

    struct pbuf *p = pbuf_alloc(PBUF_TRANSPORT, len, PBUF_RAM);
    if (p == NULL) {
        return false;
    }

    memcpy(p->payload, payload, len);

    err_t err = udp_sendto(telemetry_pcb, p, addr, port);
    pbuf_free(p);

    return err == ERR_OK;
}

bool net_send_ack(const char *payload, uint16_t len)
{
    if (!have_sender) {
        return false;
    }
    return net_send_to(&last_sender, last_sender_port, payload, len);
}

bool net_send_telemetry(const char *payload, uint16_t len)
{
    return net_send_to(&server_addr, NET_SERVER_PORT, payload, len);
}
