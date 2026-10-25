import { token } from './api.js'

// Поток телеметрии, общий на все вкладки панели.
//
// Разделы открывают в отдельных вкладках и ведут параллельно. Если бы
// каждая вкладка держала своё соединение с потоком событий, браузер
// упёрся бы в предел одновременных соединений на один адрес: шесть
// открытых вкладок — и часть из них молча висит на «поток прерван», а
// заодно задыхаются обычные запросы. Поэтому соединение держит одна
// вкладка, а остальные получают от неё пакеты через BroadcastChannel.
//
// Выбор ведущей вкладки — через Web Locks: блокировка держится, пока
// вкладка жива, и освобождается сама при её закрытии, даже если
// браузер убили. Самодельный «замок» в localStorage после падения
// вкладки оставил бы панель без потока до истечения срока.

const CHANNEL = 'mchs-telemetry'
const LOCK = 'mchs-telemetry-stream'

export function subscribe(onPacket, onStatus) {
  let stopped = false
  let source
  let retry
  let release

  const bus = 'BroadcastChannel' in window ? new BroadcastChannel(CHANNEL) : null

  const emitStatus = (status) => {
    onStatus?.(status)
    bus?.postMessage({ t: 'status', status })
  }
  const emitPacket = (packet) => {
    onPacket(packet)
    bus?.postMessage({ t: 'packet', packet })
  }

  if (bus) {
    bus.onmessage = (e) => {
      const msg = e.data
      if (msg?.t === 'packet') onPacket(msg.packet)
      else if (msg?.t === 'status') onStatus?.(msg.status)
      else if (msg?.t === 'hello' && source) {
        // Новая вкладка спрашивает, как дела с потоком: ведущая
        // отвечает, иначе вкладка висела бы на «поток прерван» до
        // первого пакета.
        bus.postMessage({ t: 'status', status: source.readyState === 1 ? 'online' : 'offline' })
      }
    }
  }

  const open = () => {
    if (stopped) return

    // EventSource не умеет заголовки, поэтому токен идёт в адресе.
    // Внутри закрытого контура по TLS это приемлемо; будь панель
    // доступна снаружи, понадобился бы отдельный одноразовый ключ.
    const t = token()
    source = new EventSource(`/api/v1/stream${t ? `?token=${encodeURIComponent(t)}` : ''}`)

    source.onopen = () => emitStatus('online')
    source.onmessage = (e) => {
      try {
        emitPacket(JSON.parse(e.data))
      } catch {
        // мусор в потоке не должен ронять панель
      }
    }
    source.onerror = () => {
      emitStatus('offline')
      source.close()
      // Канал внутри контура может моргать, центр перезапускают:
      // панель обязана подняться сама, без перезагрузки оператором.
      retry = setTimeout(open, 3000)
    }
  }

  if (bus && navigator.locks?.request) {
    navigator.locks.request(LOCK, () => new Promise((done) => {
      if (stopped) {
        done()
        return
      }
      release = done
      open()
    }))
    // Пока ведущая вкладка не ответит, считаем поток неизвестным, а
    // не оборванным: красная строка при живом потоке хуже молчания.
    bus.postMessage({ t: 'hello' })
  } else {
    // Старый браузер без общих каналов: своё соединение, как раньше.
    open()
  }

  return () => {
    stopped = true
    clearTimeout(retry)
    source?.close()
    source = null
    release?.()
    bus?.close()
  }
}
