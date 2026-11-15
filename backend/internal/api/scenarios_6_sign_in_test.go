//go:build acceptance

package api

// Signing in when several people reach the same server.
//
// "Se connecter" and "Créer un compte" are throttled, so that nobody can try
// passwords or create accounts as fast as a machine can. Each sentence below
// says who that throttle may stop and who it must not: the person trying too
// often, never a stranger who happens to reach the server through the same
// proxy. The visitors and their addresses are explained in
// acceptance_harness_test.go (visitor): in production every visitor is the
// same network peer, the frontend's nginx, and only the address the proxy
// passes on tells them apart.

import (
	"fmt"
	"net/http"
	"testing"
)

func TestScenarios_SignIn(t *testing.T) {
	playScenarios(t, "Te connecter", []scenario{
		{
			phrase: "Quand quelqu'un s'est trompé six fois de suite de mot de passe, une autre personne qui se connecte à son propre compte depuis une autre adresse y arrive du premier coup.",
			status: validated,
			run: func(t *testing.T, app *testApp) {
				app.accountWithPassword("camille@exemple.fr", "le-mot-de-passe-de-camille")
				app.accountWithPassword("dominique@exemple.fr", "le-mot-de-passe-de-dominique")

				camille := app.visitorFrom("203.0.113.7")
				for i := 1; i <= 6; i++ {
					if got := camille.signIn("camille@exemple.fr", fmt.Sprintf("pas-le-bon-%d", i)); got.code == http.StatusOK {
						app.fatalf("l'essai n°%d de Camille, avec un mauvais mot de passe, l'a fait entrer : le scénario suppose qu'elle se trompe", i)
					}
				}

				dominique := app.visitorFrom("198.51.100.4")
				dominique.expectLetIn("Dominique", "dominique@exemple.fr",
					dominique.signIn("dominique@exemple.fr", "le-mot-de-passe-de-dominique"), http.StatusOK)
			},
		},
		{
			phrase: "Quand quelqu'un essaie des mots de passe sur dix comptes différents depuis la même adresse, en joignant à chaque essai un jeton de connexion inventé, son sixième essai est refusé avec « Trop de tentatives. Patientez quelques instants. »",
			status: validated,
			run: func(t *testing.T, app *testApp) {
				accounts := make([]string, 0, 10)
				for i := 1; i <= 10; i++ {
					email := fmt.Sprintf("client%d@exemple.fr", i)
					app.accountWithPassword(email, fmt.Sprintf("le-vrai-mot-de-passe-%d", i))
					accounts = append(accounts, email)
				}

				intruder := app.visitorFrom("203.0.113.7")
				tokens := make([]string, 0, len(accounts))
				answers := make([]answer, 0, len(accounts))
				for i, email := range accounts {
					// A different made-up token on every attempt: the server
					// never issued any of them.
					token := fmt.Sprintf("jeton-invente-%d", i+1)
					tokens = append(tokens, token)
					answers = append(answers, intruder.bearing(token).signIn(email, "motdepasse123"))
				}

				for i, got := range answers[:5] {
					if got.code == http.StatusTooManyRequests {
						app.failf("l'essai n°%d (sur %s) ne devrait pas encore être refusé, c'est le sixième qui doit l'être ; le serveur a répondu %s", i+1, accounts[i], got)
					}
				}
				app.expectTooManyAttempts(fmt.Sprintf("le sixième essai (sur %s, depuis 203.0.113.7, avec le jeton inventé %q)", accounts[5], tokens[5]), answers[5])
			},
		},
		{
			phrase: "Quand quelqu'un crée des comptes en rafale depuis une adresse, une autre personne qui crée le sien depuis une autre adresse y arrive du premier coup.",
			status: validated,
			run: func(t *testing.T, app *testApp) {
				burst := app.visitorFrom("203.0.113.7")
				for i := 1; i <= 10; i++ {
					burst.signUp(fmt.Sprintf("compte%d@exemple.fr", i), "un-mot-de-passe-solide")
				}

				dominique := app.visitorFrom("198.51.100.4")
				dominique.expectLetIn("Dominique", "dominique@exemple.fr",
					dominique.signUp("dominique@exemple.fr", "le-mot-de-passe-de-dominique"), http.StatusCreated)
			},
		},
	})
}
