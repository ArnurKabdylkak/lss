import { useState } from 'react'
import { api } from '../lib/api.js'
import { credentials } from '../lib/validate.js'
import Button from './ui/Button.jsx'
import Field from './ui/Field.jsx'

// Вход оператора тренажёра.
export default function Login({ onEnter }) {
  const [login, setLogin] = useState('')
  const [password, setPassword] = useState('')
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)

  const submit = async (e) => {
    e.preventDefault()

    const invalid = credentials(login, password)
    if (invalid) {
      setError(invalid)
      return
    }

    setBusy(true)
    setError('')
    try {
      onEnter(await api.login(login, password))
    } catch (err) {
      setError(err.message)
      setPassword('')
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="flex min-h-screen items-center justify-center p-6">
      <form
        onSubmit={submit}
        className="w-full max-w-[380px] rounded-md border bg-[var(--color-surface)] p-6"
        style={{ borderColor: 'var(--color-line)' }}
      >
        <div className="mb-6 flex flex-col items-center text-center">
          <img src="/emblem.png" alt="" width="88" height="88" />
          <h1 className="mt-3 t-xl font-semibold">Тренажёр ТЖМ</h1>
          <p className="t-base text-[var(--color-ink-soft)]">Вход оператора</p>
        </div>

        <div className="space-y-4">
          <Field
            label="Логин" value={login} onChange={setLogin}
            autoFocus autoComplete="username"
          />
          <Field
            label="Пароль" value={password} onChange={setPassword}
            type="password" autoComplete="current-password"
          />
        </div>

        <Button
          variant="primary" className="mt-6 w-full"
          type="submit" disabled={busy}
        >
          {busy ? 'Проверяю…' : 'Войти'}
        </Button>

        {/* role="alert" — программа чтения с экрана произнесёт ошибку
            сразу, а не когда до неё доберутся табуляцией. */}
        {error && (
          <p
            role="alert"
            className="mt-4 rounded border px-3 py-2 t-base"
            style={{
              background: 'var(--color-alarm-bg)',
              borderColor: 'var(--color-alarm)',
              color: 'var(--color-alarm)',
            }}
          >
            {error}
          </p>
        )}
      </form>
    </div>
  )
}
