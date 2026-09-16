// Package provider is the catalog of mailbox providers Mailsorter knows how to
// reach, and of what each one demands before it lets a third party in.
//
// It exists because "add IMAP support" is not one decision but a table. Every
// provider answers four questions differently: which transport carries the
// mail, which credential opens it, which of Mailsorter's two editions is even
// allowed to use that route, and what can make it fail for a given user. Left
// implicit, those answers end up scattered across handlers as special cases.
// Held here, they are data: one table, exhaustively testable, and the UI can
// render the right instructions for each provider from the same source the
// backend connects with.
//
// The package is pure. It performs no network call and holds no credential: it
// only says what a route WOULD require. Resolving a route into a live
// connection is the job of the api layer and the transport adapters, exactly as
// internal/rules decides what a rule would do and internal/api performs it.
//
// THE EDITION RULE IS THE POINT. Mailsorter ships in two editions and they do
// not offer the same providers:
//
//   - Self-hosted: the user runs the instance and owns the credentials. They can
//     create their own Google Cloud project, which puts them inside Google's
//     personal-use exemption, so the full Gmail API is available with no
//     verification and no security assessment. They can also run Proton Bridge
//     on the same machine.
//   - Hosted: Mailsorter runs the instance and holds the credentials. A single
//     shared OAuth client is capped by Google at 100 authorizations for the
//     lifetime of the Cloud project, so the Gmail API route is NOT available
//     here at any price, and Proton is unreachable because Bridge is a local
//     process.
//
// Encoding that rule as data rather than as a comment is what stops someone
// from wiring the Gmail API into the hosted service six months from now and
// discovering the cap at user 101.
package provider

// Edition is which of the two Mailsorter distributions is running. It is set
// once at boot from configuration and decides which routes below are offered.
type Edition string

const (
	// EditionSelfHosted is the user's own instance, holding their own
	// credentials, configured with their own Google Cloud project where that
	// applies. Free, MIT, no account with Mailsorter.
	EditionSelfHosted Edition = "self-hosted"
	// EditionHosted is the managed service. Mailsorter holds the credentials,
	// pays for the AI, and bills for it.
	EditionHosted Edition = "hosted"
)

// Transport is how Mailsorter talks to the mailbox.
type Transport string

const (
	// TransportGmailAPI is the Gmail v1 REST API. Richest by far (labels,
	// Gmail search syntax, stable ids, threads, push) and the only one that
	// needs an OAuth client, which is exactly what makes it self-hosted only.
	TransportGmailAPI Transport = "gmail-api"
	// TransportGraph is Microsoft Graph. A REST API like Gmail's, with folders
	// instead of labels, and with no user cap on an unverified app, which is
	// what makes it the one API route the hosted edition can offer.
	TransportGraph Transport = "graph"
	// TransportIMAP is IMAP for reading and SMTP for sending. The universal
	// floor: every provider in this catalog except Microsoft speaks it.
	TransportIMAP Transport = "imap"
)

// AuthMethod is the credential the user hands over.
type AuthMethod string

const (
	// AuthOAuth is a provider-issued token. Revocable by the user, scoped, and
	// never equal to the account password. Always preferable when available.
	AuthOAuth AuthMethod = "oauth"
	// AuthAppPassword is a secondary credential the user generates themselves.
	// It grants FULL mailbox access, cannot be scoped, and does not expire on
	// its own. It is stored with the same AES-256-GCM sealing as the OAuth
	// tokens (see internal/api/tokens.go); nothing else would be acceptable.
	AuthAppPassword AuthMethod = "app-password"
	// AuthPassword is the mailbox password itself, which some small and
	// self-hosted providers are the only ones to still accept. Offered last,
	// and only where the provider has no better option.
	AuthPassword AuthMethod = "password"
)

// TLSMode distinguishes the two ways a mail connection is encrypted. Getting it
// wrong is the single most common cause of a connect failure that looks like a
// bad password: a STARTTLS port answers plaintext first, so an implicit-TLS
// client hangs on it, and the reverse fails the handshake.
type TLSMode string

const (
	// TLSImplicit wraps the whole session in TLS from the first byte.
	// Conventionally IMAP 993 and SMTP 465.
	TLSImplicit TLSMode = "implicit"
	// TLSSTARTTLS opens in the clear and upgrades. Conventionally SMTP 587 and
	// IMAP 143. Never accept a session that fails to upgrade.
	TLSSTARTTLS TLSMode = "starttls"
)

// Endpoint is one host and port to dial.
type Endpoint struct {
	Host string
	Port int
	TLS  TLSMode
}

// Capability is what a route can actually do, so the product degrades honestly
// instead of failing at runtime. A feature that needs a capability the route
// lacks must be hidden, not attempted: offering saved Gmail searches on an
// Orange mailbox produces an error the user cannot act on.
type Capability uint16

const (
	// CapLabels means a message can carry several labels at once. Without it
	// the mailbox is folder-based: a message is in exactly one place, so
	// "label and archive" becomes "move", and snooze needs a folder rather
	// than a label.
	CapLabels Capability = 1 << iota
	// CapProviderSearch means the provider parses Gmail search syntax, which is
	// what internal/search stores. Gmail over IMAP has it through X-GM-RAW.
	CapProviderSearch
	// CapStableIDs means a message id survives across folders and sessions, so
	// it can be a database key. IMAP UIDs are per-folder and reset on a
	// UIDVALIDITY change, so a generic IMAP route does NOT have this.
	CapStableIDs
	// CapThreads means the provider groups messages into conversations itself.
	CapThreads
	// CapServerSend means the route can send mail as the user, which the daily
	// digest needs.
	CapServerSend
	// CapPush means the provider can notify of new mail rather than being
	// polled. Nothing in Mailsorter uses it yet; it is declared so a future
	// near-real-time tier knows where it is even possible.
	CapPush
)

// Has reports whether every capability in want is present.
func (c Capability) Has(want Capability) bool { return c&want == want }

// Blocker is a reason a route can be unavailable to a particular user even
// though the provider supports it in principle. These are not edge cases: every
// one below was found to affect a real, identifiable slice of users, and each
// produces a failure that looks like a wrong password unless it is named. The
// connect screen exists to explain them BEFORE the user tries.
type Blocker string

const (
	// BlockerTwoFactorRequired: the provider only issues app passwords to
	// accounts with two-step verification already enabled.
	BlockerTwoFactorRequired Blocker = "two-factor-required"
	// BlockerAdvancedProtection: accounts in Google's Advanced Protection
	// Program cannot create app passwords at all, and enrolling revokes
	// existing ones. Same for a 2FA configured with only a passkey or only a
	// security key.
	BlockerAdvancedProtection Blocker = "advanced-protection"
	// BlockerAdminPolicy: a Workspace or Microsoft 365 administrator can
	// disable the whole route for their organization. Google offers three
	// independent switches, one of which allowlists OAuth client ids and so
	// excludes app-password access by construction.
	BlockerAdminPolicy Blocker = "admin-policy"
	// BlockerAdminConsent: the route works, but an administrator must approve
	// the application for the tenant before any of its users can connect.
	BlockerAdminConsent Blocker = "admin-consent"
	// BlockerPaidPlanRequired: the provider reserves protocol access to its
	// paying customers.
	BlockerPaidPlanRequired Blocker = "paid-plan-required"
	// BlockerLocalOnly: the route can only be reached from the machine the user
	// runs, which is what confines it to the self-hosted edition.
	BlockerLocalOnly Blocker = "local-only"
	// BlockerOwnCloudProject: the user must create their own OAuth client. This
	// is the cost of the personal-use exemption, and it includes publishing the
	// app, without which the grant expires every seven days.
	BlockerOwnCloudProject Blocker = "own-cloud-project"
	// BlockerDatacenterIP: the provider scores sign-ins on IP reputation and
	// may refuse a login arriving from a hosting provider rather than the
	// user's own network. It is the structural weakness of the hosted edition
	// and the reason the self-hosted one is not merely a licensing choice.
	BlockerDatacenterIP Blocker = "datacenter-ip"
)

// Route is one concrete way to reach a provider's mailbox.
type Route struct {
	Transport Transport
	Auth      AuthMethod
	// Editions lists which distributions may offer this route. A route with no
	// edition is unreachable and the catalog tests reject it.
	Editions []Edition
	// IMAP and SMTP are set only for TransportIMAP, and only where the provider
	// publishes stable, verified endpoints. Left nil the connection is resolved
	// at runtime by autodiscovery (RFC 6186 SRV records, then the Mozilla
	// autoconfig database), because hardcoding the settings of every mail host
	// in the world is not a table anyone can keep correct.
	IMAP *Endpoint
	SMTP *Endpoint
	Caps Capability
	// SendPerDay is the provider's published ceiling on messages sent, or 0
	// when unknown. The digest sends at most one message per user per day, so
	// this is a sanity bound rather than a budget.
	SendPerDay int
	// MaxConnections is how many simultaneous sessions the provider allows per
	// account, or 0 when unknown. It is shared with the user's own phone and
	// laptop, so Mailsorter must stay well under it rather than claim it all.
	MaxConnections int
	Blockers       []Blocker
	// Note says, in one sentence, what someone integrating this route has to
	// know and would otherwise learn from a production incident.
	Note string
}

// Offers reports whether this route is available in the given edition.
func (r Route) Offers(e Edition) bool {
	for _, allowed := range r.Editions {
		if allowed == e {
			return true
		}
	}
	return false
}

// Autodiscover reports whether this route resolves its endpoints at runtime
// rather than from the catalog.
func (r Route) Autodiscover() bool {
	return r.Transport == TransportIMAP && (r.IMAP == nil || r.SMTP == nil)
}

// Provider is one mail service and every way Mailsorter can reach it.
type Provider struct {
	Key  string
	Name string
	// Domains are the address domains that identify this provider, so a user
	// who types their address lands on the right instructions without choosing
	// from a list. Empty for the generic fallback.
	Domains []string
	// Routes are ordered best first, where "best" means least friction for the
	// user: a revocable token beats a pasted password, and an API beats IMAP.
	Routes []Route
}
