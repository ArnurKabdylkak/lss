/*
 * Конфигурация lwIP для STM32F767 + libopencm3.
 * Режим NO_SYS=1: без ОС, raw API, обработка в главном цикле.
 */
#ifndef LWIPOPTS_H
#define LWIPOPTS_H

/* --- Режим работы --- */
#define NO_SYS                      1
#define LWIP_NETCONN                0   /* требует ОС */
#define LWIP_SOCKET                 0   /* требует ОС */
#define SYS_LIGHTWEIGHT_PROT        0

/* --- Протоколы --- */
#define LWIP_ARP                    1
#define LWIP_ETHERNET               1
#define LWIP_IPV4                   1
#define LWIP_IPV6                   0
#define LWIP_ICMP                   1   /* ping для диагностики линии */
#define LWIP_UDP                    1
#define LWIP_TCP                    0   /* телеметрия идёт только по UDP */
#define LWIP_DHCP                   0   /* статический IP */
#define LWIP_DNS                    0
#define LWIP_IGMP                   0
#define LWIP_RAW                    0
#define LWIP_STATS                  0

/* --- Память ---
 * Куча и пулы подобраны под UDP-телеметрию: трафик мелкий и редкий. */
#define MEM_ALIGNMENT               4
#define MEM_SIZE                    (16 * 1024)
#define MEMP_NUM_PBUF               16
#define MEMP_NUM_UDP_PCB            4
#define MEMP_NUM_SYS_TIMEOUT        8
#define PBUF_POOL_SIZE              16
#define PBUF_POOL_BUFSIZE           1536

/* --- ARP --- */
#define ARP_TABLE_SIZE              8
#define ARP_QUEUEING                1

/* --- Контрольные суммы ---
 * ETH MAC у STM32F7 умеет считать и проверять их аппаратно
 * (включается через eth_enable_checksum_offload()), поэтому
 * программный расчёт отключён. */
#define CHECKSUM_GEN_IP             0
#define CHECKSUM_GEN_UDP            0
#define CHECKSUM_GEN_TCP            0
#define CHECKSUM_GEN_ICMP           0
#define CHECKSUM_CHECK_IP           0
#define CHECKSUM_CHECK_UDP          0
#define CHECKSUM_CHECK_TCP          0
#define CHECKSUM_CHECK_ICMP         0

/* --- Прочее --- */
#define LWIP_NETIF_HOSTNAME         1
#define LWIP_NETIF_STATUS_CALLBACK  1
#define LWIP_NETIF_LINK_CALLBACK    1
#define LWIP_SINGLE_NETIF           1
#define LWIP_DEBUG                  0

#endif /* LWIPOPTS_H */
