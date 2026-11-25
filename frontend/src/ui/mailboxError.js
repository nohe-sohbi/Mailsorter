import { useNavigate } from 'react-router-dom';
import { useToast } from './Toast';
import { apiError, reconnectAction } from '../services/api';

// What a screen says when an action on the mailbox fails: the server's own
// sentence, written once for each case server-side (a password the provider
// refuses, a mailbox that did not answer, a mail being brought back already,
// several copies of it), so every screen says the same thing. When the
// mailbox has to be connected again (428), the message carries the way:
// "Mettre à jour" to the new password form when the provider refuses the one
// on file, "Brancher une boîte" to /connect when none is connected.
export function useMailboxErrorToast() {
  const toast = useToast();
  const navigate = useNavigate();
  return (err, fallback) => {
    const message = apiError(err, fallback);
    const reconnect = reconnectAction(err);
    if (reconnect) {
      toast.action(message, reconnect.label, () => navigate(reconnect.to), { variant: 'error' });
      return;
    }
    toast.error(message);
  };
}
