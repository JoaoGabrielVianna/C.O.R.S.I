package httpapi

import (
	"net/http"
	"time"

	"github.com/corsi/backend/internal/chat/domain"
	"github.com/corsi/backend/internal/chat/ports"
	"github.com/corsi/backend/internal/platform/render"
)

// usageWindow reads the optional ?from= / ?to= bounds.
//
// RFC 3339, absolute instants, half open: from is inclusive, to exclusive.
// Deliberately not a `?period=today` shortcut — "today" depends on where
// the reader is, and a server that decides that on its own would silently
// bill a late-night turn to the wrong day. The caller knows its timezone
// and sends the two instants it means.
//
// Both bounds are optional and independent. Neither one is the lifetime
// total, which is what the two existing usage routes returned before this
// parameter existed, so old callers are unaffected.
func usageWindow(r *http.Request) (ports.UsageFilter, error) {
	var f ports.UsageFilter
	for _, b := range []struct {
		name string
		dst  **time.Time
	}{
		{"from", &f.From},
		{"to", &f.To},
	} {
		raw := r.URL.Query().Get(b.name)
		if raw == "" {
			continue
		}
		t, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			return f, domain.Invalid(b.name + " must be an RFC 3339 timestamp, for example 2026-08-09T00:00:00-03:00")
		}
		*b.dst = &t
	}
	if f.From != nil && f.To != nil && !f.To.After(*f.From) {
		return f, domain.Invalid("to must be after from")
	}
	return f, nil
}

// conversationUsage returns what one conversation consumed and cost.
func (h *Handler) conversationUsage(w http.ResponseWriter, r *http.Request) {
	ws, ok := h.workspaceID(w, r)
	if !ok {
		return
	}
	id, ok := h.pathID(w, r)
	if !ok {
		return
	}
	f, err := usageWindow(r)
	if err != nil {
		h.writeDomainErr(w, err)
		return
	}
	rep, err := h.svc.ConversationUsage(r.Context(), ws, id, f)
	if err != nil {
		h.writeDomainErr(w, err)
		return
	}
	render.JSON(w, http.StatusOK, rep)
}

// agentUsage returns what every conversation an agent owns consumed and
// cost.
func (h *Handler) agentUsage(w http.ResponseWriter, r *http.Request) {
	ws, ok := h.workspaceID(w, r)
	if !ok {
		return
	}
	id, ok := h.pathID(w, r)
	if !ok {
		return
	}
	f, err := usageWindow(r)
	if err != nil {
		h.writeDomainErr(w, err)
		return
	}
	rep, err := h.svc.AgentUsage(r.Context(), ws, id, f)
	if err != nil {
		h.writeDomainErr(w, err)
		return
	}
	render.JSON(w, http.StatusOK, rep)
}

// workspaceUsage returns the whole workspace's consumption over a window.
// This is the level a daily total is asked at; with no window it is the
// lifetime total.
func (h *Handler) workspaceUsage(w http.ResponseWriter, r *http.Request) {
	ws, ok := h.workspaceID(w, r)
	if !ok {
		return
	}
	f, err := usageWindow(r)
	if err != nil {
		h.writeDomainErr(w, err)
		return
	}
	rep, err := h.svc.WorkspaceUsage(r.Context(), ws, f)
	if err != nil {
		h.writeDomainErr(w, err)
		return
	}
	render.JSON(w, http.StatusOK, rep)
}

// providerSpend returns the real billed spend of the provider's key, read
// from the gateway. A gateway that is down surfaces as 502 upstream — the
// same mapping every other provider call uses.
func (h *Handler) providerSpend(w http.ResponseWriter, r *http.Request) {
	ws, ok := h.workspaceID(w, r)
	if !ok {
		return
	}
	id, ok := h.pathID(w, r)
	if !ok {
		return
	}
	spend, err := h.svc.ProviderSpend(r.Context(), ws, id)
	if err != nil {
		h.writeDomainErr(w, err)
		return
	}
	render.JSON(w, http.StatusOK, spend)
}
