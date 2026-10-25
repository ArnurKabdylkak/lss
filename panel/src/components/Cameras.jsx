import { useCallback, useEffect, useRef, useState } from "react";
import Status from "./ui/Status.jsx";
import { streamURL } from "../lib/api.js";

// Сетка камер объекта.
//
// Центр отдаёт список камер с состоянием трансляции и ссылкой на
// плейлист; видео приходит оттуда же (HLS). Пароли камер сюда не
// доходят — в списке только адрес трансляции.

const LABEL = {
  live: "в эфире",
  starting: "подключение",
  failed: "нет сигнала",
  disabled: "выключена",
  stopped: "остановлена",
};

const TONE = {
  live: "ok",
  starting: "idle",
  failed: "alarm",
  disabled: "idle",
  stopped: "idle",
};

export default function Cameras({ cameras }) {
  if (!cameras?.length) {
    return (
      <p className="t-base text-[var(--color-ink-soft)]">
        Камеры не настроены. Список задаётся файлом <code>cameras.yaml</code> на
        стороне центра.
      </p>
    );
  }

  // Не в эфире — наверх: пропавшая камера важнее работающей.
  const order = [...cameras].sort(
    (a, b) => Number(a.live) - Number(b.live) || a.id.localeCompare(b.id),
  );

  return (
    <div className="grid grid-cols-1 gap-4 2xl:grid-cols-2">
      {order.map((cam) => (
        <Camera key={cam.id} cam={cam} />
      ))}
    </div>
  );
}

function Camera({ cam }) {
  const [error, setError] = useState("");

  return (
    <figure
      className="overflow-hidden rounded-md border bg-[var(--color-surface)]"
      style={{ borderColor: "var(--color-line)" }}
    >
      <div className="relative bg-black">
        {cam.live ? (
          <Player cam={cam} onError={setError} />
        ) : (
          <Placeholder cam={cam} />
        )}
      </div>

      <figcaption className="flex flex-wrap items-center justify-between gap-2 px-3 py-2">
        <div>
          <p className="t-base font-medium text-[var(--color-ink)]">
            {cam.name}
          </p>
          {cam.location && (
            <p className="t-sm text-[var(--color-ink-soft)]">{cam.location}</p>
          )}
        </div>
        <Status tone={TONE[cam.status] ?? "idle"} size="sm">
          {LABEL[cam.status] ?? cam.status}
        </Status>
      </figcaption>

      {/*
        Причину молчания показываем словами: «нет сигнала» без объяснения
        заставляет дежурного идти к стойке и смотреть журнал сервиса.
      */}
      {(error || (!cam.live && cam.error)) && (
        <p
          className="border-t px-3 py-2 t-sm"
          style={{
            borderColor: "var(--color-line)",
            color: "var(--color-warn)",
          }}
        >
          {error || cam.error}
          {cam.restarts > 0 &&
            !error &&
            ` · попыток подключения: ${cam.restarts}`}
        </p>
      )}
    </figure>
  );
}

// Player держит одну трансляцию.
//
// Порядок важен: сначала hls.js, и только если он не поддерживается —
// встроенный в браузер HLS. Chromium на вопрос `canPlayType` про
// `application/vnd.apple.mpegurl` отвечает «maybe», но поток не
// играет: проверять его первым значит получить чёрный кадр везде,
// кроме Safari.
//
// Сам плеер весит больше всей панели, поэтому грузится отдельным
// файлом и только когда камеру действительно показывают. Файл лежит
// в бандле — внешних ссылок в контуре нет.
function Player({ cam, onError }) {
  const ref = useRef(null);
  const hlsRef = useRef(null);

  // Прямой эфир смотрят с края. Перемоткой туда не попасть: в MSE
  // позиция за пределами загруженного буфера просто отбрасывается.
  // Поэтому загрузку перезапускаем — startLoad(-1) начинает с живого
  // края плейлиста.
  const toLive = useCallback(() => hlsRef.current?.startLoad(-1), []);

  // Браузер вправе отказать в автозапуске. Отказ нельзя проглатывать:
  // иначе дежурный смотрит на вечный индикатор загрузки и считает,
  // что сломалась камера.
  const [blocked, setBlocked] = useState(false);

  // Счётчик пересборок плеера. Увеличение роняет и собирает заново
  // весь конвейер вместе с буфером MSE: после перезапуска ffmpeg
  // начинает новый прогон с нулевой отметки времени, и буфер старого
  // прогона с ним несовместим — плеер исправно качает сегменты, но
  // картинка стоит. Вылечить это можно только чистым буфером.
  const [attempt, setAttempt] = useState(0);
  const rebuild = useCallback(() => setAttempt((n) => n + 1), []);

  useEffect(() => {
    const video = ref.current;
    if (!video) return;

    const src = streamURL(cam.playlist);
    onError("");

    let hls;
    let dropped = false;
    let retry;
    // Номер первого сегмента в плейлисте: по его откату назад видно,
    // что эфир пошёл с начала.
    let seen = null;

    // Браузер не начинает воспроизведение сам: автозапуск разрешён
    // только беззвучному видео и только явным вызовом. Отказ не ломает
    // картинку — у плеера есть кнопка.
    const play = () =>
      video
        .play()
        .then(() => setBlocked(false))
        .catch(() => {
          // Пока зритель не нажал, копить буфер незачем: окно
          // плейлиста уедет вперёд, а накопленное устареет и даст
          // «поток прервался» при первом же запуске.
          hlsRef.current?.stopLoad();
          setBlocked(true);
        });

    const native = () => {
      video.src = src;
      video.addEventListener("loadedmetadata", play, { once: true });
    };

    import("hls.js")
      .then(({ default: Hls }) => {
        // Камеру успели закрыть, пока грузился плеер.
        if (dropped) return;

        if (!Hls.isSupported()) {
          if (video.canPlayType("application/vnd.apple.mpegurl")) native();
          else onError("Браузер не умеет показывать этот поток");
          return;
        }

        hls = new Hls({
          lowLatencyMode: true,
          // Дежурному нужен текущий кадр, а не то, что было минуту
          // назад: плеер не отстаёт от края эфира больше чем на пару
          // сегментов.
          liveSyncDurationCount: 2,
          maxLiveSyncPlaybackRate: 1.5,
          manifestLoadingMaxRetry: 6,
          levelLoadingMaxRetry: 6,
          fragLoadingMaxRetry: 6,
        });

        hls.on(Hls.Events.ERROR, (_, data) => {
          if (!data.fatal) return;

          // Сеть внутри контура моргает, а камера может
          // перезапускаться: плеер обязан подняться сам, без
          // вмешательства оператора.
          switch (data.type) {
            case Hls.ErrorTypes.NETWORK_ERROR:
              onError("Поток прервался, переподключаюсь…");
              // Сначала просто продолжить с живого края: моргнувшую
              // сеть этого хватает пережить, не теряя картинку.
              hls.startLoad(-1);
              // Если за это время эфир не вернулся, пересобираем
              // плеер целиком: значит прогон сменился.
              clearTimeout(retry);
              retry = setTimeout(rebuild, 4000);
              break;
            case Hls.ErrorTypes.MEDIA_ERROR:
              onError("Сбой декодирования, восстанавливаю…");
              hls.recoverMediaError();
              break;
            default:
              onError("Поток недоступен");
              hls.destroy();
          }
        });

        hls.on(Hls.Events.MANIFEST_PARSED, play);
        hls.on(Hls.Events.FRAG_BUFFERED, () => {
          clearTimeout(retry);
          onError("");
        });

        // Плейлист начался заново — ffmpeg пошёл новым прогоном.
        // Ошибки при этом может не быть вовсе: сегменты качаются,
        // но ложатся мимо буфера, поэтому ловим сам откат нумерации.
        hls.on(Hls.Events.LEVEL_UPDATED, (_, data) => {
          const first = data.details?.startSN;
          if (first == null) return;
          if (seen != null && first < seen) {
            onError("Поток начался заново, переподключаюсь…");
            rebuild();
            return;
          }
          seen = first;
        });

        hlsRef.current = hls;
        hls.loadSource(src);
        hls.attachMedia(video);
      })
      .catch(() => onError("Не удалось загрузить плеер"));

    return () => {
      dropped = true;
      clearTimeout(retry);
      video.removeEventListener("loadedmetadata", play);
      hlsRef.current = null;
      hls?.destroy();
      if (video.src) {
        video.removeAttribute("src");
        video.load();
      }
    };
    // Пересобираем плеер только при смене адреса: лишний перезапуск
    // роняет картинку на секунду.
  }, [cam.playlist, onError, rebuild, attempt]);

  return (
    <>
      <video
        ref={ref}
        muted
        autoPlay
        playsInline
        controls
        aria-label={`Камера: ${cam.name}`}
        className="aspect-video w-full bg-black object-cover"
      />

      {blocked && (
        <button
          type="button"
          onClick={() => {
            // Начинаем с живого края, а не с того, что успело
            // накопиться до нажатия.
            toLive();
            ref.current
              ?.play()
              .then(() => setBlocked(false))
              .catch(() => {});
          }}
          className="absolute inset-0 flex flex-col items-center justify-center gap-2 bg-black/60 text-white"
        >
          <span aria-hidden="true" className="text-3xl leading-none">
            ▶
          </span>
          <span className="t-base">Показать поток</span>
          <span className="t-sm text-slate-300">
            браузер не разрешил запуск без нажатия
          </span>
        </button>
      )}
    </>
  );
}

function Placeholder({ cam }) {
  return (
    <div className="flex aspect-video w-full items-center justify-center">
      <p className="px-4 text-center t-base text-slate-400">
        {cam.status === "disabled"
          ? "Камера выключена в настройках"
          : cam.status === "starting"
            ? "Подключение к камере…"
            : "Нет сигнала"}
      </p>
    </div>
  );
}
