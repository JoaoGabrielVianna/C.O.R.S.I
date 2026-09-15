package tools

import (
	"context"

	chatdomain "github.com/corsi/backend/internal/chat/domain"
	"github.com/corsi/backend/internal/palace/domain"
)

/* ── shaping ─────────────────────────────────────────────────────────── */

func sessionOut(s *domain.Session) map[string]any {
	out := map[string]any{
		"session_id":         s.ID.String(),
		"status":             string(s.Status),
		"active_room_id":     idPtr(s.ActiveRoomID),
		"active_artifact_id": idPtr(s.ActiveArtifactID),
		"started_at":         stamp(s.StartedAt),
		"last_activity_at":   stamp(s.LastActivityAt),
		"closed_at":          stampPtr(s.ClosedAt),
	}
	if s.Summary != "" {
		out["summary"] = s.Summary
	}
	return out
}

/* ── palace.session.get ──────────────────────────────────────────────── */

type sessionGet struct{ base }

func (sessionGet) Definition() chatdomain.ToolDefinition {
	return chatdomain.ToolDefinition{
		Name:         SessionGetTool,
		Title:        "Palace · Contexto atual",
		Effect:       chatdomain.EffectRead,
		Confidential: true,
		Description: "Reads the working context: whether a stretch of work is open and " +
			"what it is pointed at. Call it when the user says something like \"volta pro " +
			"que eu estava fazendo\" or when a request depends on what they are currently " +
			"inside. It reports that there is none when nothing is open, which is an " +
			"ordinary answer and not a failure. Reading it does not count as activity.",
		Schema: chatdomain.ToolSchema{
			Properties: map[string]chatdomain.ToolProperty{
				// The schema must declare at least one property, and there is
				// genuinely nothing to ask: the workspace comes from the
				// context and there is at most one open session in it. A
				// boolean that changes nothing would be worse than this, so
				// the one property is an explicit acknowledgement.
				"confirm": {
					Type: chatdomain.TypeBoolean,
					Description: "Ignored. This capability takes no input: the working context " +
						"is whatever is open now.",
				},
			},
		},
	}
}

func (t sessionGet) Execute(ctx context.Context, args map[string]any) (chatdomain.ToolOutput, error) {
	ws, err := workspaceOf(ctx)
	if err != nil {
		return nil, err
	}
	session, err := t.svc.GetOpenSession(ctx, ws)
	if err != nil {
		var de *domain.Error
		if errAs(err, &de) && de.Kind == domain.KindNotFound {
			// An ordinary state, reported as a result rather than as a
			// refusal: a model told "not found" would apologise for
			// something that is simply true.
			return fit(map[string]any{
				"open": false,
				"note": "there is no open working context. Start one with " +
					string(SessionStartTool) + " when the user settles into something.",
			}, "")
		}
		return nil, toolError(err)
	}
	return fit(map[string]any{"open": true, "session": sessionOut(session)}, "")
}

/* ── palace.session.start ────────────────────────────────────────────── */

type sessionStart struct{ base }

func (sessionStart) Definition() chatdomain.ToolDefinition {
	return chatdomain.ToolDefinition{
		Name:         SessionStartTool,
		Title:        "Palace · Abrir contexto",
		Effect:       chatdomain.EffectWrite,
		Confidential: true,
		Description: "Opens a stretch of work, when the user settles into something " +
			"(\"vamos trabalhar nesse projeto\"). Safe to call more than once: if one is " +
			"already open it hands back that one and says so, without disturbing it. " +
			"There is one working context at a time. It starts pointed at nothing; say " +
			"what it is about with " + string(SessionFocusTool) + ".",
		Schema: chatdomain.ToolSchema{
			Properties: map[string]chatdomain.ToolProperty{
				"confirm": {
					Type: chatdomain.TypeBoolean,
					Description: "Ignored. This capability takes no input: there is one working " +
						"context per workspace.",
				},
			},
		},
	}
}

func (t sessionStart) Execute(ctx context.Context, args map[string]any) (chatdomain.ToolOutput, error) {
	ws, err := workspaceOf(ctx)
	if err != nil {
		return nil, err
	}
	res, err := t.svc.StartSession(ctx, ws)
	if err != nil {
		return nil, toolError(err)
	}
	return fit(map[string]any{
		"session":      sessionOut(res.Session),
		"created":      !res.AlreadyOpen,
		"already_open": res.AlreadyOpen,
	}, "")
}

/* ── palace.session.focus ────────────────────────────────────────────── */

type sessionFocus struct{ base }

func (sessionFocus) Definition() chatdomain.ToolDefinition {
	return chatdomain.ToolDefinition{
		Name:         SessionFocusTool,
		Title:        "Palace · Mudar o foco",
		Effect:       chatdomain.EffectWrite,
		Confidential: true,
		Description: "Points the open working context at an area, at an object, or both, " +
			"and records what this stretch is about. The two references move ONLY when " +
			"you name them: setting the object does not set the area, even when the " +
			"object is filed in one, because where somebody is working is not the same " +
			"as what owns what. Send only what is changing; a request that changes " +
			"nothing is reported as such and is not treated as activity.",
		Schema: chatdomain.ToolSchema{
			Properties: map[string]chatdomain.ToolProperty{
				"room_id": {
					Type:        chatdomain.TypeString,
					Description: "Optional. Point the context at this area.",
					MaxLength:   36,
				},
				"detach_room": detachProperty("area"),
				"artifact_id": {
					Type:        chatdomain.TypeString,
					Description: "Optional. Point the context at this object.",
					MaxLength:   36,
				},
				"detach_artifact": detachProperty("object"),
				"summary": {
					Type: chatdomain.TypeString,
					Description: "Optional. What this stretch of work is about, in the user's " +
						"words. Send an empty string to clear it. This is working context, " +
						"not durable knowledge: it is not kept as a record when the context " +
						"closes.",
					MaxLength: domain.MaxSessionSummary,
				},
			},
		},
	}
}

func (t sessionFocus) Execute(ctx context.Context, args map[string]any) (chatdomain.ToolOutput, error) {
	ws, err := workspaceOf(ctx)
	if err != nil {
		return nil, err
	}

	room, err := optionalRefArg(args, "room_id", "detach_room")
	if err != nil {
		return nil, err
	}
	artifact, err := optionalRefArg(args, "artifact_id", "detach_artifact")
	if err != nil {
		return nil, err
	}

	res, err := t.svc.FocusSession(ctx, ws, domain.SessionFocus{
		Room:     room,
		Artifact: artifact,
		Summary:  argStringPtr(args, "summary"),
	})
	if err != nil {
		return nil, toolError(err)
	}
	return fit(map[string]any{
		"session":   sessionOut(res.Session),
		"changed":   !res.Unchanged(),
		"unchanged": res.Unchanged(),
	}, "")
}

/* ── palace.session.close ────────────────────────────────────────────── */

type sessionClose struct{ base }

func (sessionClose) Definition() chatdomain.ToolDefinition {
	return chatdomain.ToolDefinition{
		Name:         SessionCloseTool,
		Title:        "Palace · Fechar contexto",
		Effect:       chatdomain.EffectWrite,
		Confidential: true,
		Description: "Ends the open stretch of work. It closes the working context and " +
			"NOTHING ELSE: it does not turn the session's summary into a record, does not " +
			"create anything, and does not edit anything. If something from this stretch " +
			"is worth keeping, keep it deliberately with " + string(MemoryCreateTool) +
			" before closing. Calling it when nothing is open reports that there was " +
			"nothing to close, which is an answer and not a failure.",
		Schema: chatdomain.ToolSchema{
			Properties: map[string]chatdomain.ToolProperty{
				"confirm": {
					Type:        chatdomain.TypeBoolean,
					Description: "Ignored. This capability takes no input.",
				},
			},
		},
	}
}

func (t sessionClose) Execute(ctx context.Context, args map[string]any) (chatdomain.ToolOutput, error) {
	ws, err := workspaceOf(ctx)
	if err != nil {
		return nil, err
	}
	res, err := t.svc.CloseSession(ctx, ws)
	if err != nil {
		return nil, toolError(err)
	}
	if !res.Closed {
		return fit(map[string]any{
			"closed": false,
			"reason": res.Reason.String(),
			"note": "there was no open working context, so nothing was closed. Say that " +
				"plainly rather than reporting that you closed something.",
		}, "")
	}
	return fit(map[string]any{"closed": true, "session": sessionOut(res.Session)}, "")
}
