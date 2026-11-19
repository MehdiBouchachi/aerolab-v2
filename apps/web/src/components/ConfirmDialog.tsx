import { Tr } from '../app/language'
import { useEffect, useId, useRef, useState } from 'react'
import { ApiError } from '../api/client'
import { ErrorNote } from './ui'

export function ConfirmDialog({ title, description, confirmLabel, onConfirm, onClose }: {
  title: string
  description: string
  confirmLabel: string
  onConfirm: () => Promise<void>
  onClose: () => void
}) {
  const dialog = useRef<HTMLDialogElement>(null)
  const titleId = useId()
  const descriptionId = useId()
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<ApiError | null>(null)
  useEffect(() => {
    const element = dialog.current
    element?.showModal()
    return () => element?.close()
  }, [])

  const confirm = async () => {
    if (busy) return
    setBusy(true)
    setError(null)
    try {
      await onConfirm()
      onClose()
    } catch (cause) {
      setError(cause as ApiError)
      setBusy(false)
    }
  }

  return <dialog ref={dialog} className="app-modal-dialog unit-action-dialog" aria-labelledby={titleId} aria-describedby={descriptionId}
    onCancel={event => { event.preventDefault(); if (!busy) onClose() }}>
    <div className="app-modal-form">
      <header className="app-modal-header"><h2 id={titleId}>{title}</h2></header>
      <div className="app-modal-body">
        <p id={descriptionId} className="muted">{description}</p>
        <ErrorNote error={error} />
      </div>
      <footer className="app-modal-footer">
        <button type="button" autoFocus disabled={busy} onClick={onClose}><Tr>Cancel</Tr></button>
        <button type="button" className="danger" disabled={busy} onClick={() => void confirm()}>{busy ? 'Working…' : confirmLabel}</button>
      </footer>
    </div>
  </dialog>
}
