#include <string.h>

#include <libopencm3/ethernet/mac.h>
#include <libopencm3/ethernet/mac_stm32fxx7.h>
#include <libopencm3/ethernet/phy.h>
#include <libopencm3/ethernet/phy_lan87xx.h>
#include <libopencm3/stm32/gpio.h>
#include <libopencm3/stm32/rcc.h>
#include <libopencm3/stm32/syscfg.h>

#include "lwip/etharp.h"
#include "lwip/pbuf.h"
#include "lwip/snmp.h"
#include "netif/ethernet.h"

#include "ethernetif.h"

/* SYSCFG_PMC.MII_RMII_SEL — выбор RMII вместо MII. */
#define SYSCFG_PMC_MII_RMII_SEL (1 << 23)

/* MAC-адрес устройства. Локально администрируемый (бит 1 первого байта). */
static const uint8_t mac_address[6] = {0x02, 0x00, 0x00, 0x00, 0x00, 0x01};

/*
 * Дескрипторы и буферы ETH DMA.
 *
 * Линкер libopencm3 для F767 размещает .bss в SRAM1 с 0x20010000, а DTCM
 * вынесена в отдельный регион `ccm`. Это важно: ETH DMA не имеет доступа
 * к DTCM, так что обычное статическое размещение здесь корректно.
 * D-Cache в проекте не включается, поэтому обслуживание когерентности
 * кэша не требуется.
 */
static uint8_t eth_dma_area[
    ETH_TX_BUF_COUNT * (ETH_TX_BUF_SIZE + ETH_DES_STD_SIZE) +
    ETH_RX_BUF_COUNT * (ETH_RX_BUF_SIZE + ETH_DES_STD_SIZE)]
    __attribute__((aligned(16)));

/* Промежуточный буфер: libopencm3 отдаёт/принимает линейный кадр. */
static uint8_t frame_buf[ETH_RX_BUF_SIZE];

static void eth_gpio_setup(void)
{
    rcc_periph_clock_enable(RCC_GPIOA);
    rcc_periph_clock_enable(RCC_GPIOB);
    rcc_periph_clock_enable(RCC_GPIOC);
    rcc_periph_clock_enable(RCC_GPIOG);

    /* RMII на Nucleo-F767ZI, все пины AF11, максимальная скорость фронта:
     *   PA1  RMII_REF_CLK   PA2  RMII_MDIO    PA7  RMII_CRS_DV
     *   PC1  RMII_MDC       PC4  RMII_RXD0    PC5  RMII_RXD1
     *   PG11 RMII_TX_EN     PG13 RMII_TXD0    PB13 RMII_TXD1
     */
    const uint32_t pa = GPIO1 | GPIO2 | GPIO7;
    const uint32_t pb = GPIO13;
    const uint32_t pc = GPIO1 | GPIO4 | GPIO5;
    const uint32_t pg = GPIO11 | GPIO13;

    gpio_mode_setup(GPIOA, GPIO_MODE_AF, GPIO_PUPD_NONE, pa);
    gpio_set_output_options(GPIOA, GPIO_OTYPE_PP, GPIO_OSPEED_100MHZ, pa);
    gpio_set_af(GPIOA, GPIO_AF11, pa);

    gpio_mode_setup(GPIOB, GPIO_MODE_AF, GPIO_PUPD_NONE, pb);
    gpio_set_output_options(GPIOB, GPIO_OTYPE_PP, GPIO_OSPEED_100MHZ, pb);
    gpio_set_af(GPIOB, GPIO_AF11, pb);

    gpio_mode_setup(GPIOC, GPIO_MODE_AF, GPIO_PUPD_NONE, pc);
    gpio_set_output_options(GPIOC, GPIO_OTYPE_PP, GPIO_OSPEED_100MHZ, pc);
    gpio_set_af(GPIOC, GPIO_AF11, pc);

    gpio_mode_setup(GPIOG, GPIO_MODE_AF, GPIO_PUPD_NONE, pg);
    gpio_set_output_options(GPIOG, GPIO_OTYPE_PP, GPIO_OSPEED_100MHZ, pg);
    gpio_set_af(GPIOG, GPIO_AF11, pg);
}

static void eth_hw_init(void)
{
    rcc_periph_clock_enable(RCC_SYSCFG);

    /* Выбор RMII делается до включения тактирования ETH. */
    SYSCFG_PMC |= SYSCFG_PMC_MII_RMII_SEL;

    eth_gpio_setup();

    rcc_periph_clock_enable(RCC_ETHMAC);
    rcc_periph_clock_enable(RCC_ETHMACTX);
    rcc_periph_clock_enable(RCC_ETHMACRX);

    rcc_periph_reset_pulse(RST_ETHMAC);

    /* Делитель MDC от HCLK. При HCLK = 216 МГц нужен /102: у F7 это
     * значение CR покрывает диапазон 150-216 МГц (имя enum в libopencm3
     * унаследовано от F4, где верхняя граница 168 МГц). */
    eth_init(ETH_PHY_ADDR, ETH_CLK_150_168MHZ);

    eth_set_mac(mac_address);
    eth_desc_init(eth_dma_area, ETH_TX_BUF_COUNT, ETH_RX_BUF_COUNT,
                  ETH_TX_BUF_SIZE, ETH_RX_BUF_SIZE, false);

    /* Контрольные суммы IP/UDP/TCP считает и проверяет MAC —
     * в lwipopts.h программный расчёт поэтому выключен. */
    eth_enable_checksum_offload();

    eth_start();
}

/* Передача кадра: lwIP отдаёт цепочку pbuf, MAC ждёт линейный буфер. */
static err_t low_level_output(struct netif *netif, struct pbuf *p)
{
    (void)netif;

    if (p->tot_len > sizeof(frame_buf)) {
        return ERR_MEM;
    }

    uint16_t len = pbuf_copy_partial(p, frame_buf, p->tot_len, 0);
    if (len != p->tot_len) {
        return ERR_BUF;
    }

    if (!eth_tx(frame_buf, len)) {
        /* Все TX-дескрипторы заняты — пусть верхний уровень повторит. */
        return ERR_IF;
    }

    MIB2_STATS_NETIF_ADD(netif, ifoutoctets, len);

    return ERR_OK;
}

/* Приём одного кадра из DMA-кольца в свежий pbuf. NULL, если кадров нет. */
static struct pbuf *low_level_input(struct netif *netif)
{
    uint32_t len = 0;

    LWIP_UNUSED_ARG(netif);

    if (!eth_rx(frame_buf, &len, sizeof(frame_buf)) || len == 0) {
        return NULL;
    }

    struct pbuf *p = pbuf_alloc(PBUF_RAW, (uint16_t)len, PBUF_POOL);
    if (p == NULL) {
        /* Кадр уже вычитан из кольца, восстановить его нельзя — теряем. */
        MIB2_STATS_NETIF_INC(netif, ifindiscards);
        return NULL;
    }

    if (pbuf_take(p, frame_buf, (uint16_t)len) != ERR_OK) {
        pbuf_free(p);
        return NULL;
    }

    MIB2_STATS_NETIF_ADD(netif, ifinoctets, len);

    return p;
}

void ethernetif_poll(struct netif *netif)
{
    struct pbuf *p;

    /* Выгребаем всё кольцо целиком: за один тик могло прийти несколько кадров. */
    while ((p = low_level_input(netif)) != NULL) {
        if (netif->input(p, netif) != ERR_OK) {
            pbuf_free(p);
        }
    }
}

void ethernetif_check_link(struct netif *netif)
{
    bool up = phy_link_isup(ETH_PHY_ADDR);

    if (up && !netif_is_link_up(netif)) {
        netif_set_link_up(netif);
    } else if (!up && netif_is_link_up(netif)) {
        netif_set_link_down(netif);
    }
}

err_t ethernetif_init(struct netif *netif)
{
    LWIP_ASSERT("netif != NULL", (netif != NULL));

#if LWIP_NETIF_HOSTNAME
    netif->hostname = "stm32f767";
#endif

    netif->name[0] = 'e';
    netif->name[1] = 'n';
    netif->output = etharp_output;
    netif->linkoutput = low_level_output;

    netif->hwaddr_len = ETH_HWADDR_LEN;
    memcpy(netif->hwaddr, mac_address, ETH_HWADDR_LEN);
    netif->mtu = 1500;
    netif->flags = NETIF_FLAG_BROADCAST | NETIF_FLAG_ETHARP | NETIF_FLAG_ETHERNET;

    eth_hw_init();

    /* Сброс PHY и автосогласование скорости/дуплекса.
     * eth_init() уже перевёл MAC в режим 100 Мбит/с full duplex, что
     * соответствует результату автонегоциации с типовым коммутатором. */
    phy_reset(ETH_PHY_ADDR);

    return ERR_OK;
}
