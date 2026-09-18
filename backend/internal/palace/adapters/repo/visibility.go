package repo

// The containment predicates, in SQL, in one place.
//
// ══════════════════════════════════════════════════════════════════════
//
//	VISUAL CONTAINMENT INHERITS VISIBILITY, NOT SENSITIVITY
//
// ══════════════════════════════════════════════════════════════════════
//
// If a surface may not show a Room, it may not show what is filed in that
// Room either. Nothing here writes, copies or derives a sensitivity: the
// artifact in a withheld room is still `normal`, and moving it somewhere
// visible makes it appear again with no edit to the row. What these
// clauses decide is whether a READ may select it.
//
// ── Why this is SQL and not a filter in Go ─────────────────────────────
// Because the rule has to apply BEFORE `LIMIT`, before `OFFSET` and
// inside `count(*)`, and a filter over rows that already came back cannot
// do any of the three. Dropping withheld rows afterwards would produce a
// page of eighteen with a total of twenty, an offset that skips rows the
// caller never saw, and a count that announces the existence of exactly
// what the withholding is for. A row that is never selected is also one
// that cannot be logged, traced or serialised on its way past.
//
// ── Why the two repositories share this file ───────────────────────────
// Because the artifact rule is a SUBEXPRESSION of the memory rule. A
// memory is withheld when the artifact it is about is withheld, which
// includes that artifact being in a withheld room. Written twice, the two
// copies would agree today and diverge the first time one of them is
// edited, and the divergence would be silent: both would still return
// rows, just not the same rows.
//
// ── The aliases, and why every table gets one ──────────────────────────
// `palace.rooms`, `palace.artifacts` and `palace.memories` all carry
// `workspace_id` and `id`. A correlated subquery over two of them with
// either side unqualified resolves to whichever scope Postgres finds
// first, which is a bug that compiles, runs, and returns plausible rows.
// So every reference in this file is qualified, and every alias is
// distinct across the whole nesting:
//
//	a    the artifact being filtered            (outer, artifact reads)
//	ar   the room that contains it              (subquery of a)
//	m    the memory being filtered              (outer, memory reads)
//	mr   the room that contains the memory      (subquery of m)
//	ma   the artifact the memory is about       (subquery of m)
//	mar  the room that contains THAT artifact   (subquery of ma)

import (
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/corsi/backend/internal/palace/domain"
	"github.com/corsi/backend/internal/palace/ports"
)

// containment carries what the clauses need to be built: the placeholder
// holding the withheld level, or empty when this read is not restricting
// levels at all.
//
// ── Why the level travels as a placeholder and not a literal ───────────
// One bind, reused by every clause in the statement. Interpolating the
// word would put a vocabulary term inside a string that is assembled by
// concatenation, which is the shape of the bug this codebase has no
// appetite for, even where the value is a constant today.
type containment struct {
	// levelBind is "$3" or similar. Empty means the caller opted into
	// every level, so a container is withheld only when it is gone.
	levelBind string
}

// roomOf is the condition "the room referenced by <child>.room_id is one
// this read may show".
//
// ── Why a NULL reference passes ────────────────────────────────────────
// Because nothing contains it. An unfiled row inherits from no room, and
// treating absence as suspicious would hide the ordinary state in which
// something exists before anybody decides where it belongs. That is the
// explicit carve-out in D1: `room_id IS NULL` is evaluated on the row's
// own rules and nothing else.
//
// ── Why `deleted_at IS NULL` is in here ────────────────────────────────
// Because the question is whether the container is something this surface
// could itself show, and a soft-deleted room is not. Nothing writes that
// column in this version, so the clause costs nothing today and is right
// on the day something does.
func (c containment) roomOf(child, roomAlias string) string {
	conds := []string{
		roomAlias + ".workspace_id = " + child + ".workspace_id",
		roomAlias + ".id = " + child + ".room_id",
		roomAlias + ".deleted_at IS NULL",
	}
	if c.levelBind != "" {
		conds = append(conds, roomAlias+".sensitivity <> "+c.levelBind)
	}
	return fmt.Sprintf(
		"(%s.room_id IS NULL OR EXISTS (SELECT 1 FROM palace.rooms %s WHERE %s))",
		child, roomAlias, strings.Join(conds, " AND "))
}

// artifactEligible is the condition "<alias> is an artifact this read may
// show", written against an artifact row that is already in scope.
//
// It is the whole of D1 in one expression: the row is live, its own level
// passes, and its room passes. The memory rule embeds this rather than
// restating it, which is what keeps the transitive hop from drifting out
// of step with the direct one.
func (c containment) artifactEligible(alias, roomAlias string) string {
	conds := []string{alias + ".deleted_at IS NULL"}
	if c.levelBind != "" {
		conds = append(conds, alias+".sensitivity <> "+c.levelBind)
	}
	conds = append(conds, c.roomOf(alias, roomAlias))
	return "(" + strings.Join(conds, " AND ") + ")"
}

// artifactContainment is the clause an ARTIFACT read adds: its own room
// must be showable.
//
// The row's own level is not repeated here, because the filter already
// applies it to the outer table. This clause is only about the container.
func (c containment) artifactContainment(alias string) string {
	return c.roomOf(alias, "ar")
}

// memoryContainment is the clause a MEMORY read adds: D1 on its room, and
// D1.1 on the artifact it is about.
//
// ── The case this exists for ───────────────────────────────────────────
//
//	Memory(normal) → Artifact(normal) → Room(highly_sensitive)
//
// Every row in that chain says `normal` about itself except the last one,
// and the memory has to disappear completely. Checking only the memory's
// own room would leave it visible, describing something the surface is
// refusing to show.
func (c containment) memoryContainment(alias string) string {
	return "(" + strings.Join([]string{
		c.roomOf(alias, "mr"),
		fmt.Sprintf(
			"(%[1]s.artifact_id IS NULL OR EXISTS ("+
				"SELECT 1 FROM palace.artifacts ma "+
				"WHERE ma.workspace_id = %[1]s.workspace_id "+
				"AND ma.id = %[1]s.artifact_id AND %[2]s))",
			alias, c.artifactEligible("ma", "mar")),
	}, " AND ") + ")"
}

/* ── binding ─────────────────────────────────────────────────────────── */

// withheldLevel is the one level these clauses ever compare against. It is
// named here rather than at each call site so that a fourth level, or a
// change to which level is withheld, lands in one edit.
func withheldLevel() string { return string(domain.SensitivityHighlySensitive) }

/* ── grouped reads ───────────────────────────────────────────────────── */

// scanRoomCounts reads a `GROUP BY room_id` result.
//
// Shared by the artifact and the memory grouping, because the two produce
// the same shape and the NULL group means the same thing in both: the rows
// filed nowhere. A second copy would be a second place to get the nil
// handling wrong.
func scanRoomCounts(rows pgx.Rows, op string) ([]ports.RoomCount, error) {
	var out []ports.RoomCount
	for rows.Next() {
		var (
			roomID *uuid.UUID
			n      int64
		)
		if err := rows.Scan(&roomID, &n); err != nil {
			return nil, safeDBError("scan "+op, err)
		}
		out = append(out, ports.RoomCount{RoomID: roomID, Count: n})
	}
	if err := rows.Err(); err != nil {
		return nil, safeDBError(op, err)
	}
	return out, nil
}
