import { describe, expect, it, vi } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { Login } from './pages/Login'
import { Chart } from './components/Chart'

const signIn = vi.fn()
vi.mock('./app/context', () => ({ useApp: () => ({ signIn }) }))

describe('Login', () => {
  it('validates on blur and does not call the server with empty fields', async () => {
    render(<Login />)
    fireEvent.blur(screen.getByLabelText('Username'))
    expect(screen.getByText('Enter your username.')).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: 'Sign in' }))
    expect(signIn).not.toHaveBeenCalled()
  })
  it('explains a wrong password without blaming the user', async () => {
    signIn.mockRejectedValueOnce(Object.assign(new Error('x'), { status: 401 }))
    render(<Login />)
    fireEvent.change(screen.getByLabelText('Username'), { target: { value: 'admin' } })
    fireEvent.change(screen.getByLabelText('Password'), { target: { value: 'nope' } })
    fireEvent.click(screen.getByRole('button', { name: 'Sign in' }))
    await waitFor(() => expect(screen.getByRole('alert')).toHaveTextContent('do not match'))
  })
})

describe('Chart', () => {
  it('says so instead of drawing a flat line when there is no valid data', () => {
    render(<Chart title="pH" keyName="ph" from="2026-11-12T10:00:00Z" to="2026-11-12T11:00:00Z" gaps={[]}
      points={[{ t: '2026-11-12T10:30:00Z', snapshots: 1, time_quality: 'DEVICE_CLOCK', readings: { ph: { value: null, quality: 'invalid' } } }]} />)
    expect(screen.getByText(/No valid readings/)).toBeInTheDocument()
    expect(document.querySelector('polyline')).toBeNull()
  })
  it('renders a gap band and two line segments around an outage', () => {
    const p = (t: string, v: number) => ({ t, snapshots: 1, time_quality: 'DEVICE_CLOCK', readings: { ph: { value: v, quality: 'valid' as const } } })
    const { container } = render(<Chart title="pH" keyName="ph" from="2026-11-12T10:00:00Z" to="2026-11-12T11:00:00Z" gaps={[{ from: '2026-11-12T10:02:00Z', to: '2026-11-12T10:50:00Z' }]}
      points={[p('2026-11-12T10:00:00Z', 6), p('2026-11-12T10:01:00Z', 6.1), p('2026-11-12T10:02:00Z', 6.1), p('2026-11-12T10:50:00Z', 6.2), p('2026-11-12T10:51:00Z', 6.3)]} />)
    expect(container.querySelectorAll('polyline')).toHaveLength(2)
    expect(container.querySelectorAll('rect.gap')).toHaveLength(1)
  })
})
