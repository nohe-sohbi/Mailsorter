import React, { createContext, useCallback, useContext, useRef, useState } from 'react';
import Modal from './Modal';
import Spinner from './Spinner';
import { Alert } from './icons';

const ConfirmContext = createContext(null);

// A promise-shaped confirmation, so a caller reads as ordinary control flow:
//
//   if (!(await confirm({ title: '…', danger: true }))) return;
//
// It exists because the destructive paths had nothing in front of them: one
// click on a sender's trash icon deleted every email that sender ever sent, the
// `a` shortcut applied every pending suggestion including deletions, and
// removing a rule was instant and unrecoverable.
//
// `typeToConfirm` raises the bar for the truly irreversible ones: the user has
// to type the expected word, which is the difference between a reflex and a
// decision.
export function ConfirmProvider({ children }) {
  const [state, setState] = useState(null);
  const [typed, setTyped] = useState('');
  const [busy, setBusy] = useState(false);
  const resolveRef = useRef(null);
  const confirmButtonRef = useRef(null);

  const confirm = useCallback((options = {}) => {
    setTyped('');
    setBusy(false);
    setState({
      title: 'Confirmer',
      message: '',
      confirmLabel: 'Confirmer',
      cancelLabel: 'Annuler',
      danger: false,
      typeToConfirm: null,
      ...options,
    });
    return new Promise((resolve) => {
      resolveRef.current = resolve;
    });
  }, []);

  const settle = useCallback((value) => {
    resolveRef.current?.(value);
    resolveRef.current = null;
    setState(null);
    setTyped('');
    setBusy(false);
  }, []);

  const onConfirm = useCallback(() => {
    // Guard against a double-submit closing the dialog twice.
    if (busy) return;
    setBusy(true);
    settle(true);
  }, [busy, settle]);

  const needsTyping = Boolean(state?.typeToConfirm);
  const canConfirm = !needsTyping || typed.trim().toLowerCase() === state.typeToConfirm.toLowerCase();

  return (
    <ConfirmContext.Provider value={confirm}>
      {children}
      <Modal
        open={Boolean(state)}
        onClose={() => settle(false)}
        title={state?.title}
        size="sm"
        // Focus the confirm button only when there is nothing to type; otherwise
        // the field is what the user needs.
        initialFocusRef={needsTyping ? undefined : confirmButtonRef}
        footer={
          state && (
            <>
              <button onClick={() => settle(false)} className="btn-secondary w-full sm:w-auto">
                {state.cancelLabel}
              </button>
              <button
                ref={confirmButtonRef}
                onClick={onConfirm}
                disabled={!canConfirm || busy}
                className={(state.danger ? 'btn-danger' : 'btn-primary') + ' w-full sm:w-auto'}
              >
                {busy && <Spinner size={16} />}
                {state.confirmLabel}
              </button>
            </>
          )
        }
      >
        {state && (
          <div className="flex gap-3">
            {state.danger && (
              <span className="flex h-10 w-10 shrink-0 items-center justify-center rounded-xl bg-danger-50 text-danger-600">
                <Alert size={20} />
              </span>
            )}
            <div className="min-w-0 flex-1 space-y-3">
              {state.message && <p className="text-sm text-ink-700">{state.message}</p>}
              {state.detail && <p className="text-xs text-muted">{state.detail}</p>}
              {needsTyping && (
                <label className="block">
                  <span className="mb-1.5 block text-xs font-semibold text-ink-700">
                    Tapez <span className="font-mono text-danger-600">{state.typeToConfirm}</span> pour confirmer
                  </span>
                  <input
                    className="input"
                    value={typed}
                    onChange={(e) => setTyped(e.target.value)}
                    onKeyDown={(e) => {
                      if (e.key === 'Enter' && canConfirm) onConfirm();
                    }}
                    autoComplete="off"
                    aria-label={`Tapez ${state.typeToConfirm} pour confirmer`}
                  />
                </label>
              )}
            </div>
          </div>
        )}
      </Modal>
    </ConfirmContext.Provider>
  );
}

export function useConfirm() {
  const ctx = useContext(ConfirmContext);
  if (!ctx) throw new Error('useConfirm must be used within a ConfirmProvider');
  return ctx;
}
