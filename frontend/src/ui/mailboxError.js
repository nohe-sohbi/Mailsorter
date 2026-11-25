import { useNavigate } from 'react-router-dom';
import { useToast } from './Toast';
import { apiError } from '../services/api';

// What a screen says when an action on the mailbox fails: the server's own
// sentence, written once for each case server-side (a password the provider
// refuses, a mailbox that did not answer, a mail being brought back already,
// several copies of it), so every screen says the same thing. When the
// mailbox has to be connected again (428: none is, or the provider now
// refuses its password), the message carries "Mettre à jour", which leads to
// /connect.
export function useMailboxErrorToast() {
  const toast = useToast();
  const navigate = useNavigate();
  return (err, fallback) => {
    const message = apiError(err, fallback);
    if (err?.response?.status === 428) {
      toast.action(message, 'Mettre à jour', () => navigate('/connect'), { variant: 'error' });
      return;
    }
    toast.error(message);
  };
}
