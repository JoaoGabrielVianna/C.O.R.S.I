// @vitest-environment jsdom

import { afterEach, describe, expect, it } from "vitest";
import { cleanup, render, screen } from "@testing-library/react";

import { UserMessage } from "./MessageBubble";
import { I18nFixture } from "@/lib/i18n";

/**
 * What the transcript says about a turn's attachments.
 *
 * The property under test is not that chips render — it is that the
 * transcript renders WHAT WAS STORED, never what the catalogue says today.
 * A turn whose tool was later revoked, renamed or removed from the build
 * still has to report what was attached to it.
 *
 * ── Stored per capability, read back per integration ───────────────────
 * The record is one row per capability, because that is what was really in
 * scope and what an audit has to be able to reconstruct. The chip is one
 * per provider, because that is the choice the person made. Both are true
 * at once, and the grouping is derivation over the stored rows — nothing
 * here consults a catalogue, which is why it keeps working for a provider
 * this build no longer has.
 */

afterEach(cleanup);

describe("a user turn's attachments", () => {
  it("shows one chip per integration, from the capabilities it froze", () => {
    render(
      <I18nFixture lang="pt">
        <UserMessage
          message={{
            content: "o que mudou?",
            references: [
              { kind: "tool", id: "github.repository.list", label: "GitHub · Repositórios" },
              { kind: "tool", id: "github.commit.list", label: "GitHub · Commits" },
              { kind: "tool", id: "job_radar.opportunity.list", label: "Job Radar · Listar oportunidades" },
            ],
          }}
        />
      </I18nFixture>,
    );

    const chips = screen.getAllByRole("listitem");
    expect(chips).toHaveLength(2);
    expect(screen.getByText("GitHub")).toBeTruthy();
    expect(screen.getByText("Job Radar")).toBeTruthy();
    // The scope is recoverable from the transcript itself, not only from
    // the database: the chip carries the capabilities it stands for.
    expect(chips[0].getAttribute("title")).toContain("github.repository.list");
    expect(chips[0].getAttribute("title")).toContain("github.commit.list");
  });

  it("keeps naming the capabilities a turn really had, whatever ships later", () => {
    // A turn from before an eighth GitHub capability existed. Nothing here
    // resolves anything, so nothing about it can change when one does.
    render(
      <I18nFixture lang="pt">
        <UserMessage
          message={{
            content: "antigo",
            references: [{ kind: "tool", id: "github.commit.list", label: "GitHub · Commits" }],
          }}
        />
      </I18nFixture>,
    );
    const chip = screen.getByRole("listitem");
    expect(chip.getAttribute("title")).toBe("github.commit.list");
    expect(screen.getByText("GitHub")).toBeTruthy();
  });

  it("renders a stored label that no catalogue could resolve today", () => {
    // The revoked, renamed or deleted case. Nothing here looks anything up,
    // which is exactly why this keeps working.
    render(
      <I18nFixture lang="pt">
        <UserMessage
          message={{
            content: "usei enquanto existia",
            references: [{ kind: "tool", id: "gone.forever", label: "Sistema Antigo · Ferramenta" }],
          }}
        />
      </I18nFixture>,
    );
    // The provider reads from the stored label, and the identity from the
    // stored name. Neither exists in this build any more.
    expect(screen.getByText("Sistema Antigo")).toBeTruthy();
    expect(screen.getByRole("listitem").getAttribute("title")).toBe("gone.forever");
  });

  it("shows nothing at all for a turn that attached nothing", () => {
    const { container } = render(
      <I18nFixture lang="pt">
        <UserMessage message={{ content: "pergunta comum" }} />
      </I18nFixture>,
    );
    // Absent means "no selection was made", which is the legacy turn. An
    // empty row, a dash or a "nenhuma" would be the interface inventing a
    // statement the backend never made.
    expect(container.querySelectorAll("li")).toHaveLength(0);
  });

  it("shows nothing for an explicitly empty list, for the same reason", () => {
    const { container } = render(
      <I18nFixture lang="pt">
        <UserMessage message={{ content: "pergunta comum", references: [] }} />
      </I18nFixture>,
    );
    expect(container.querySelectorAll("li")).toHaveLength(0);
  });
});
