package api

import (
	"net/http"

	"github.com/nohe-sohbi/mailsorter/backend/internal/provider"
)

// The mailbox catalog, served to the connect screen.
//
// The frontend knows no provider. It renders what this endpoint returns, which
// is the same table internal/api connects with, filtered to the running
// edition. That is the whole point: a provider offered on screen but
// unreachable by the backend, or reachable but never offered, are both bugs
// that cannot happen if there is one source and it is this one.
//
// The payload is a DTO rather than the catalog structs: those carry no json
// tags, and the repo's API contract is camelCase. Shaping it here also keeps
// the wire format stable when the internal table grows a field.

type providerView struct {
	Key     string      `json:"key"`
	Name    string      `json:"name"`
	Domains []string    `json:"domains,omitempty"`
	Routes  []routeView `json:"routes"`
}

type routeView struct {
	Transport string `json:"transport"`
	Auth      string `json:"auth"`
	// Autodiscover tells the screen not to promise settings it does not have:
	// the connection is resolved from the domain when the user connects.
	Autodiscover bool           `json:"autodiscover"`
	IMAP         *endpointView  `json:"imap,omitempty"`
	SMTP         *endpointView  `json:"smtp,omitempty"`
	Capabilities capabilityView `json:"capabilities"`
	// Blockers are why a connection can fail for this particular user. The
	// screen shows them BEFORE the attempt, because every one of them otherwise
	// surfaces as an authentication error the user cannot act on.
	Blockers []string `json:"blockers,omitempty"`
	Note     string   `json:"note"`
}

type endpointView struct {
	Host string `json:"host"`
	Port int    `json:"port"`
	TLS  string `json:"tls"`
}

// capabilityView spells the bitmask out as booleans. The frontend must not
// reimplement bit arithmetic to decide whether to show the saved-searches tab.
type capabilityView struct {
	Labels         bool `json:"labels"`
	ProviderSearch bool `json:"providerSearch"`
	StableIDs      bool `json:"stableIds"`
	Threads        bool `json:"threads"`
	Send           bool `json:"send"`
	Push           bool `json:"push"`
}

// GetProviders returns the mailbox providers this instance can actually reach.
//
// Public on purpose: in the self-hosted edition the connect screen is the first
// thing a visitor sees, before any account exists. It carries no secret, only
// provider names, hostnames, ports and help text.
func (h *Handler) GetProviders(w http.ResponseWriter, r *http.Request) {
	catalog := provider.ForEdition(Edition)
	views := make([]providerView, 0, len(catalog))

	for _, p := range catalog {
		routes := make([]routeView, 0, len(p.Routes))
		for _, rt := range p.Routes {
			blockers := make([]string, 0, len(rt.Blockers))
			for _, b := range rt.Blockers {
				blockers = append(blockers, string(b))
			}
			routes = append(routes, routeView{
				Transport:    string(rt.Transport),
				Auth:         string(rt.Auth),
				Autodiscover: rt.Autodiscover(),
				IMAP:         endpointOf(rt.IMAP),
				SMTP:         endpointOf(rt.SMTP),
				Capabilities: capabilityView{
					Labels:         rt.Caps.Has(provider.CapLabels),
					ProviderSearch: rt.Caps.Has(provider.CapProviderSearch),
					StableIDs:      rt.Caps.Has(provider.CapStableIDs),
					Threads:        rt.Caps.Has(provider.CapThreads),
					Send:           rt.Caps.Has(provider.CapServerSend),
					Push:           rt.Caps.Has(provider.CapPush),
				},
				Blockers: blockers,
				Note:     rt.Note,
			})
		}
		views = append(views, providerView{
			Key:     p.Key,
			Name:    p.Name,
			Domains: p.Domains,
			Routes:  routes,
		})
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"edition":   string(Edition),
		"providers": views,
	})
}

func endpointOf(e *provider.Endpoint) *endpointView {
	if e == nil {
		return nil
	}
	return &endpointView{Host: e.Host, Port: e.Port, TLS: string(e.TLS)}
}
