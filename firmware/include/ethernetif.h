/*
 * Драйвер netif для lwIP поверх ETH MAC STM32F7 (libopencm3).
 *
 * Плата Nucleo-F767ZI, PHY LAN8742A на RMII, адрес PHY = 0.
 */
#ifndef ETHERNETIF_H
#define ETHERNETIF_H

#include <stdbool.h>
#include <stdint.h>

#include "lwip/err.h"
#include "lwip/netif.h"

/* Адрес PHY на MDIO-шине (Nucleo-F767ZI: LAN8742A = 0). */
#define ETH_PHY_ADDR 0

/* Число и размер DMA-буферов приёма/передачи. */
#define ETH_RX_BUF_COUNT 4
#define ETH_RX_BUF_SIZE  1524
#define ETH_TX_BUF_COUNT 4
#define ETH_TX_BUF_SIZE  1524

/* Функция инициализации, передаётся в netif_add(). */
err_t ethernetif_init(struct netif *netif);

/* Забрать все принятые кадры и отдать их в lwIP. Вызывать из главного цикла. */
void ethernetif_poll(struct netif *netif);

/* Опросить PHY и синхронизировать состояние линка с netif.
 * Вызывать не чаще чем раз в ~100 мс. */
void ethernetif_check_link(struct netif *netif);

#endif /* ETHERNETIF_H */
