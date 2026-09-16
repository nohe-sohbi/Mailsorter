package provider

import (
	"errors"
	"strings"
)

// Provider keys. Constants rather than bare strings, like every other
// vocabulary in this repo: a key is stored on a user's mailbox record and a
// typo would silently orphan a connection.
const (
	KeyGmail      = "gmail"
	KeyWorkspace  = "google-workspace"
	KeyOutlook    = "outlook"
	KeyMicrosoft  = "microsoft-365"
	KeyOrange     = "orange"
	KeyFree       = "free"
	KeyLaPoste    = "laposte"
	KeySFR        = "sfr"
	KeyYahoo      = "yahoo"
	KeyICloud     = "icloud"
	KeyFastmail   = "fastmail"
	KeyZoho       = "zoho"
	KeyInfomaniak = "infomaniak"
	KeyOVH        = "ovh"
	KeyProton     = "proton"
	KeyGeneric    = "imap"
)

var (
	// ErrUnknownProvider is returned for a key no catalog entry carries.
	ErrUnknownProvider = errors.New("provider: unknown provider")
	// ErrNotInEdition is returned when the provider exists but no route of its
	// own is available in the running edition. It is a product answer, not a
	// bug: Gmail's API route in the hosted edition and Proton anywhere but
	// self-hosted both land here, and the UI must say why rather than fail.
	ErrNotInEdition = errors.New("provider: no route available in this edition")
)

// bothEditions is the common case and is written once so a reader can see at a
// glance which routes are the exceptions.
var bothEditions = []Edition{EditionSelfHosted, EditionHosted}

// catalog is the table. Order inside Routes is significant: best first.
//
// A note on what is deliberately ABSENT. There is no OAuth-over-IMAP route for
// Gmail. Gmail accepts exactly one scope for XOAUTH2, https://mail.google.com/,
// which is the broadest restricted scope Google publishes. Taking it would
// carry every verification obligation the app is trying to escape while
// granting more access than it has today: strictly worse on both axes. If a
// future contributor reaches for it, this paragraph is the answer.
var catalog = []Provider{
	{
		Key:     KeyGmail,
		Name:    "Gmail",
		Domains: []string{"gmail.com", "googlemail.com"},
		Routes: []Route{
			{
				Transport: TransportGmailAPI,
				Auth:      AuthOAuth,
				// Self-hosted ONLY, and this is the load-bearing line of the
				// whole catalog. A shared OAuth client is capped by Google at
				// 100 authorizations for the lifetime of the Cloud project,
				// non-resettable. The cap is a property of the project, not of
				// the code, so it vanishes when each user owns their project,
				// which only the self-hosted edition can arrange.
				Editions:       []Edition{EditionSelfHosted},
				Caps:           CapLabels | CapProviderSearch | CapStableIDs | CapThreads | CapServerSend | CapPush,
				SendPerDay:     500,
				MaxConnections: 0,
				Blockers:       []Blocker{BlockerOwnCloudProject},
				Note: "Pleine fidelite. L'utilisateur cree son projet Google Cloud, ce qui le place dans " +
					"l'exemption d'usage personnel: aucune verification, aucun audit. Il DOIT publier " +
					"l'application, sans quoi l'autorisation expire tous les sept jours.",
			},
			{
				Transport: TransportIMAP,
				Auth:      AuthAppPassword,
				Editions:  bothEditions,
				IMAP:      &Endpoint{Host: "imap.gmail.com", Port: 993, TLS: TLSImplicit},
				SMTP:      &Endpoint{Host: "smtp.gmail.com", Port: 587, TLS: TLSSTARTTLS},
				// Gmail's X-GM-EXT-1 extensions are why this route keeps almost
				// the whole product: X-GM-LABELS reads and writes labels and
				// creates missing ones, X-GM-RAW runs Gmail search syntax
				// verbatim, and X-GM-MSGID is the same integer as the API's
				// message id in a different base, so the stored primary key
				// survives the migration.
				Caps:           CapLabels | CapProviderSearch | CapStableIDs | CapThreads | CapServerSend,
				SendPerDay:     500,
				MaxConnections: 15,
				Blockers: []Blocker{
					BlockerTwoFactorRequired,
					BlockerAdvancedProtection,
					BlockerDatacenterIP,
				},
				Note: "Aucune surface de conformite: pas de client OAuth, donc rien a verifier et aucun " +
					"plafond. Les limites reelles sont ailleurs: environ 2,5 Go par jour et par compte, " +
					"partages avec le telephone et l'ordinateur de l'utilisateur, donc ne jamais " +
					"telecharger le corps d'un message sans qu'une regle le demande.",
			},
		},
	},
	{
		Key:  KeyWorkspace,
		Name: "Google Workspace",
		// No Domains: a Workspace mailbox lives on the customer's own domain,
		// so it cannot be recognized from the address. The user picks it.
		Routes: []Route{
			{
				Transport:  TransportGmailAPI,
				Auth:       AuthOAuth,
				Editions:   []Edition{EditionSelfHosted},
				Caps:       CapLabels | CapProviderSearch | CapStableIDs | CapThreads | CapServerSend | CapPush,
				SendPerDay: 2000,
				Blockers:   []Blocker{BlockerOwnCloudProject, BlockerAdminPolicy},
				Note: "La meilleure route du catalogue pour une entreprise: en application interne a " +
					"l'organisation, Google exempte de la verification ET du plafond, sans ecran " +
					"d'avertissement. L'organisation possede le projet Cloud, ce qui suppose " +
					"l'auto-hebergement.",
			},
			{
				Transport:      TransportIMAP,
				Auth:           AuthAppPassword,
				Editions:       bothEditions,
				IMAP:           &Endpoint{Host: "imap.gmail.com", Port: 993, TLS: TLSImplicit},
				SMTP:           &Endpoint{Host: "smtp.gmail.com", Port: 587, TLS: TLSSTARTTLS},
				Caps:           CapLabels | CapProviderSearch | CapStableIDs | CapThreads | CapServerSend,
				SendPerDay:     2000,
				MaxConnections: 15,
				Blockers: []Blocker{
					BlockerTwoFactorRequired,
					BlockerAdminPolicy,
					BlockerAdvancedProtection,
					BlockerDatacenterIP,
				},
				Note: "Peu fiable en entreprise: l'administrateur dispose de trois interrupteurs " +
					"independants, dont un qui restreint l'IMAP aux clients OAuth approuves et exclut " +
					"donc les mots de passe d'application par construction. Detecter les trois " +
					"separement plutot que d'afficher une erreur d'authentification.",
			},
		},
	},
	{
		Key:     KeyOutlook,
		Name:    "Outlook.com",
		Domains: []string{"outlook.com", "outlook.fr", "hotmail.com", "hotmail.fr", "live.com", "live.fr", "msn.com"},
		Routes: []Route{
			{
				Transport: TransportGraph,
				Auth:      AuthOAuth,
				Editions:  bothEditions,
				// Folders, not labels: a message sits in exactly one place, so
				// snooze moves it to a folder and "etiqueter puis archiver"
				// becomes a single move.
				Caps:           CapStableIDs | CapThreads | CapServerSend | CapPush,
				MaxConnections: 4,
				Note: "Le seul fournisseur ou l'edition hebergee garde l'ergonomie actuelle: un bouton, " +
					"un jeton revocable, aucun mot de passe stocke, et surtout AUCUN plafond " +
					"d'utilisateurs sur une application non verifiee. L'envoi passe par Graph, jamais " +
					"par SMTP: Microsoft a supprime l'authentification simple sur IMAP et POP en " +
					"septembre 2024, il n'existe donc pas de route par mot de passe d'application.",
			},
		},
	},
	{
		Key:  KeyMicrosoft,
		Name: "Microsoft 365",
		Routes: []Route{
			{
				Transport:      TransportGraph,
				Auth:           AuthOAuth,
				Editions:       bothEditions,
				Caps:           CapStableIDs | CapThreads | CapServerSend | CapPush,
				MaxConnections: 4,
				Blockers:       []Blocker{BlockerAdminConsent},
				Note: "Les permissions de courrier ne sont pas en consentement libre dans un locataire " +
					"d'entreprise: un administrateur doit approuver l'application pour tout le " +
					"locataire. C'est une etape humaine par client, pas une barriere Microsoft, et " +
					"il n'existe aucun equivalent payant de l'audit Google.",
			},
		},
	},
	{
		Key:     KeyOrange,
		Name:    "Orange",
		Domains: []string{"orange.fr", "wanadoo.fr"},
		Routes: []Route{
			{
				Transport: TransportIMAP,
				Auth:      AuthAppPassword,
				Editions:  bothEditions,
				IMAP:      &Endpoint{Host: "imap.orange.fr", Port: 993, TLS: TLSImplicit},
				SMTP:      &Endpoint{Host: "smtp.orange.fr", Port: 465, TLS: TLSImplicit},
				Caps:      CapServerSend,
				Note: "La plus grande boite non-Gmail de France, et aucune approbation a demander. " +
					"Orange appelle son identifiant une cle d'acces, generee dans l'espace securite du " +
					"compte: ce n'est pas le mot de passe du compte, et c'est la premiere source de " +
					"confusion a l'inscription.",
			},
		},
	},
	{
		Key:     KeyLaPoste,
		Name:    "La Poste",
		Domains: []string{"laposte.net"},
		Routes: []Route{
			{
				Transport: TransportIMAP,
				Auth:      AuthAppPassword,
				Editions:  bothEditions,
				IMAP:      &Endpoint{Host: "imap.laposte.net", Port: 993, TLS: TLSImplicit},
				SMTP:      &Endpoint{Host: "smtp.laposte.net", Port: 587, TLS: TLSSTARTTLS},
				Caps:      CapServerSend,
				Note:      "TLS obligatoire des deux cotes. Noter le SMTP en 587 STARTTLS, la ou Orange est en 465 implicite.",
			},
		},
	},
	{
		Key:     KeyICloud,
		Name:    "iCloud Mail",
		Domains: []string{"icloud.com", "me.com", "mac.com"},
		Routes: []Route{
			{
				Transport: TransportIMAP,
				Auth:      AuthAppPassword,
				Editions:  bothEditions,
				IMAP:      &Endpoint{Host: "imap.mail.me.com", Port: 993, TLS: TLSImplicit},
				SMTP:      &Endpoint{Host: "smtp.mail.me.com", Port: 587, TLS: TLSSTARTTLS},
				Caps:      CapServerSend,
				Blockers:  []Blocker{BlockerTwoFactorRequired},
				Note: "Apple n'offre ni OAuth ni API publique. Le mot de passe dedie n'est affiche qu'une " +
					"seule fois a sa creation, ce qui en fait la pire inscription du catalogue: " +
					"prevoir de le dire avant, pas apres.",
			},
		},
	},
	{
		Key:     KeyFastmail,
		Name:    "Fastmail",
		Domains: []string{"fastmail.com", "fastmail.fr"},
		Routes: []Route{
			{
				Transport: TransportIMAP,
				Auth:      AuthAppPassword,
				Editions:  bothEditions,
				IMAP:      &Endpoint{Host: "imap.fastmail.com", Port: 993, TLS: TLSImplicit},
				SMTP:      &Endpoint{Host: "smtp.fastmail.com", Port: 465, TLS: TLSImplicit},
				Caps:      CapServerSend,
				Note:      "Public techniquement a l'aise, qui paie deja pour son courrier. Le meilleur terrain de recette pour l'adaptateur IMAP generique.",
			},
		},
	},
	{
		Key:     KeyInfomaniak,
		Name:    "Infomaniak",
		Domains: []string{"ik.me", "etik.com", "ikmail.com"},
		Routes: []Route{
			{
				Transport: TransportIMAP,
				Auth:      AuthPassword,
				Editions:  bothEditions,
				IMAP:      &Endpoint{Host: "mail.infomaniak.com", Port: 993, TLS: TLSImplicit},
				SMTP:      &Endpoint{Host: "mail.infomaniak.com", Port: 587, TLS: TLSSTARTTLS},
				Caps:      CapServerSend,
				Note:      "Hebergeur suisse, donnees en Europe: l'argument de confidentialite que les outils americains ne peuvent pas tenir.",
			},
		},
	},
	{
		Key:     KeyZoho,
		Name:    "Zoho Mail",
		Domains: []string{"zoho.com", "zohomail.com", "zoho.eu"},
		Routes: []Route{
			{
				Transport: TransportIMAP,
				Auth:      AuthAppPassword,
				Editions:  bothEditions,
				Caps:      CapServerSend,
				Blockers:  []Blocker{BlockerPaidPlanRequired, BlockerTwoFactorRequired},
				Note: "Zoho a retire l'IMAP aux nouveaux comptes gratuits. Les comptes payants le " +
					"gardent, ce qui preselectionne des utilisateurs qui paient deja pour leur courrier.",
			},
		},
	},
	{
		Key:     KeyYahoo,
		Name:    "Yahoo Mail",
		Domains: []string{"yahoo.com", "yahoo.fr", "ymail.com", "aol.com"},
		Routes: []Route{
			{
				Transport: TransportIMAP,
				Auth:      AuthAppPassword,
				Editions:  bothEditions,
				Caps:      CapServerSend,
				Blockers:  []Blocker{BlockerTwoFactorRequired},
				Note:      "Mot de passe genere dans la section connexions externes du compte. Aucune inscription developpeur.",
			},
		},
	},
	{
		Key:     KeyFree,
		Name:    "Free",
		Domains: []string{"free.fr"},
		Routes: []Route{
			{
				Transport: TransportIMAP,
				Auth:      AuthPassword,
				Editions:  bothEditions,
				Caps:      CapServerSend,
				Note:      "Reglages resolus par autodecouverte: les hotes n'ont pas ete verifies a la source et une valeur fausse dans ce tableau coute plus cher qu'une resolution au moment de la connexion.",
			},
		},
	},
	{
		Key:     KeySFR,
		Name:    "SFR",
		Domains: []string{"sfr.fr", "neuf.fr", "cegetel.net"},
		Routes: []Route{
			{
				Transport: TransportIMAP,
				Auth:      AuthPassword,
				Editions:  bothEditions,
				Caps:      CapServerSend,
				Note:      "Reglages resolus par autodecouverte, comme Free.",
			},
		},
	},
	{
		Key:  KeyOVH,
		Name: "OVH",
		Routes: []Route{
			{
				Transport: TransportIMAP,
				Auth:      AuthPassword,
				Editions:  bothEditions,
				Caps:      CapServerSend,
				Note:      "Boites sur domaine client: l'adresse ne dit pas l'hebergeur, donc autodecouverte obligatoire.",
			},
		},
	},
	{
		Key:     KeyProton,
		Name:    "Proton Mail",
		Domains: []string{"proton.me", "protonmail.com", "pm.me"},
		Routes: []Route{
			{
				Transport: TransportIMAP,
				Auth:      AuthPassword,
				// Self-hosted only, and not by choice: Proton Bridge is a
				// desktop process that binds IMAP to 127.0.0.1 on the user's
				// own machine. There is nothing for a remote server to dial.
				Editions: []Edition{EditionSelfHosted},
				IMAP:     &Endpoint{Host: "127.0.0.1", Port: 1143, TLS: TLSSTARTTLS},
				SMTP:     &Endpoint{Host: "127.0.0.1", Port: 1025, TLS: TLSSTARTTLS},
				Caps:     CapServerSend,
				Blockers: []Blocker{BlockerLocalOnly, BlockerPaidPlanRequired},
				Note: "Le seul fournisseur que l'edition hebergee ne pourra jamais servir, et la " +
					"meilleure illustration de ce que l'auto-hebergement debloque: Mailsorter tourne " +
					"sur la machine ou tourne Bridge, donc il voit une boite que personne d'autre ne " +
					"peut voir.",
			},
		},
	},
	{
		Key:  KeyGeneric,
		Name: "Autre fournisseur IMAP",
		Routes: []Route{
			{
				Transport: TransportIMAP,
				Auth:      AuthPassword,
				Editions:  bothEditions,
				Caps:      CapServerSend,
				Note: "Le filet: tout serveur IMAP, y compris auto-heberge. Les reglages sont resolus " +
					"par les enregistrements SRV du domaine puis par la base de configuration " +
					"Mozilla, et l'utilisateur peut toujours les saisir a la main.",
			},
		},
	},
}

// All returns the whole catalog. The slice is shared, so callers must not
// mutate it; it is read-only data by contract, like the rules vocabulary.
func All() []Provider { return catalog }

// ForEdition returns the providers that have at least one route in the given
// edition, each carrying only the routes that edition may use. This is what the
// connect screen renders: it can never offer a provider the running edition
// cannot actually reach.
func ForEdition(e Edition) []Provider {
	out := make([]Provider, 0, len(catalog))
	for _, p := range catalog {
		routes := make([]Route, 0, len(p.Routes))
		for _, r := range p.Routes {
			if r.Offers(e) {
				routes = append(routes, r)
			}
		}
		if len(routes) == 0 {
			continue
		}
		p.Routes = routes
		out = append(out, p)
	}
	return out
}

// Lookup returns a provider by key.
func Lookup(key string) (Provider, error) {
	for _, p := range catalog {
		if p.Key == key {
			return p, nil
		}
	}
	return Provider{}, ErrUnknownProvider
}

// Detect guesses the provider from an email address, so a user types their
// address and gets the right instructions instead of picking from a list of
// fifteen. It falls back to the generic IMAP entry, which is always a valid
// answer rather than a failure.
func Detect(address string) Provider {
	at := strings.LastIndex(address, "@")
	if at < 0 || at == len(address)-1 {
		return genericProvider()
	}
	domain := strings.ToLower(strings.TrimSpace(address[at+1:]))
	for _, p := range catalog {
		for _, d := range p.Domains {
			if d == domain {
				return p
			}
		}
	}
	return genericProvider()
}

func genericProvider() Provider {
	p, err := Lookup(KeyGeneric)
	if err != nil {
		// Unreachable: the catalog tests assert the generic entry exists.
		return Provider{Key: KeyGeneric, Name: "Autre fournisseur IMAP"}
	}
	return p
}

// Pick returns the route Mailsorter should use for a provider in an edition:
// the first one the provider declares, since routes are ordered best first.
//
// It is the single place that answers "can this edition talk to this provider",
// so the answer cannot drift between the connect screen, the background loops
// and the billing page.
func Pick(key string, e Edition) (Route, error) {
	p, err := Lookup(key)
	if err != nil {
		return Route{}, err
	}
	for _, r := range p.Routes {
		if r.Offers(e) {
			return r, nil
		}
	}
	return Route{}, ErrNotInEdition
}
