// Проверка введённых чисел.
//
// Границы намеренно повторяют серверные (telemetry/command.go) и
// прошивку. Дублирование здесь оправдано: оператор должен видеть
// ошибку в момент ввода, а не после похода команды до платы и обратно.
// Проверка в панели — удобство, а не защита: решение всё равно
// перепроверяют центр, сборщик и плата.

export const LIMITS = {
  // Тепловая защита пушки.
  limitC: { min: 1, max: 125, step: 0.5, unit: '°C' },
  restoreC: { min: 1, max: 125, step: 0.5, unit: '°C' },

  // Длина окружности колеса: от детского 12" до 29" с запасом.
  wheelMM: { min: 900, max: 2500, step: 1, unit: 'мм' },

  // Период отчётов телеметрии.
  periodMS: { min: 50, max: 60000, step: 10, unit: 'мс' },
}

// number проверяет одно значение по границам.
// Возвращает текст ошибки или пустую строку.
export function number(raw, spec, label) {
  if (raw === '' || raw === null || raw === undefined) {
    return `${label}: значение не введено`
  }

  // Запятая как разделитель — обычное дело при вводе с клавиатуры.
  const value = typeof raw === 'number' ? raw : Number(String(raw).replace(',', '.'))

  if (!Number.isFinite(value)) {
    return `${label}: это не число`
  }
  if (value < spec.min || value > spec.max) {
    return `${label}: допустимо от ${spec.min} до ${spec.max} ${spec.unit}`
  }
  return ''
}

// heaterLimits проверяет пару уставок целиком.
//
// Гистерезис проверяется здесь, а не покомпонентно: каждое значение по
// отдельности может быть допустимым, а пара — нет.
export function heaterLimits(limitC, restoreC) {
  const first = number(limitC, LIMITS.limitC, 'Порог отключения')
  if (first) return first

  const second = number(restoreC, LIMITS.restoreC, 'Температура возврата')
  if (second) return second

  const limit = Number(String(limitC).replace(',', '.'))
  const restore = Number(String(restoreC).replace(',', '.'))

  if (restore >= limit) {
    return `Температура возврата (${restore}) должна быть ниже порога (${limit}): ` +
      'без гистерезиса реле будет щёлкать на каждом измерении'
  }
  // Слишком узкий гистерезис не спасает от дребезга: шум датчика
  // BMP280 сопоставим с этим интервалом.
  if (limit - restore < 2) {
    return 'Между порогом и возвратом должно быть не меньше 2 °C: ' +
      'иначе шум датчика будет дёргать реле'
  }
  return ''
}

// wheel проверяет длину окружности колеса.
export function wheel(mm) {
  const err = number(mm, LIMITS.wheelMM, 'Длина окружности колеса')
  if (err) {
    const value = Number(String(mm).replace(',', '.'))
    // Самая частая ошибка ввода: сантиметры или диаметр вместо
    // окружности. Ошибка в десять раз искажает скорость всего заезда.
    if (Number.isFinite(value) && value > 0 && value < LIMITS.wheelMM.min) {
      return `${err}. Нужна длина ОКРУЖНОСТИ в миллиметрах ` +
        '(для 700×23c это 2096 мм), а не диаметр и не сантиметры'
    }
    return err
  }
  return ''
}

// code проверяет код, набираемый на стойке.
//
// Границы те же, что в прошивке: короче трёх цифр подбирается мгновенно,
// длиннее двенадцати на матричной клавиатуре не набирают.
export function code(value) {
  const raw = String(value ?? '')

  if (raw.length < 3 || raw.length > 12) {
    return 'Код должен быть длиной от 3 до 12 цифр'
  }
  if (!/^[0-9]+$/.test(raw)) {
    return 'Код набирается на цифровой клавиатуре: в нём могут быть только цифры'
  }
  return ''
}

// login проверяет форму входа до отправки.
export function credentials(login, password) {
  if (!login.trim()) return 'Введите логин'
  if (!password) return 'Введите пароль'
  return ''
}
