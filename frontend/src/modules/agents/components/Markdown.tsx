import { memo, useMemo } from "react";
import ReactMarkdown, { type Components } from "react-markdown";
import remarkGfm from "remark-gfm";
import { Check, Copy } from "lucide-react";
import { cn } from "@/lib/utils";
import { useCopy } from "@/modules/agents/hooks/useCopy";
import { useT } from "@/lib/i18n";

/**
 * Markdown renderer for assistant text.
 *
 * ── Why the block split ────────────────────────────────────────────────
 * During streaming this component re-renders on every animation frame,
 * and re-parsing a long answer each time is the difference between a
 * smooth stream and a stuttering one. The text is split into top-level
 * blocks separated by blank lines; each block is memoized on its own
 * string. Appending to the last paragraph then re-parses only that
 * paragraph, leaving the twenty blocks above it untouched.
 *
 * ── Safety ─────────────────────────────────────────────────────────────
 * `react-markdown` does not render raw HTML unless a rehype-raw plugin is
 * added, and none is. Model output is therefore text, never markup. Do
 * not add `rehype-raw` here without a sanitizer.
 */

/**
 * Splits on blank lines, keeping fenced code blocks whole — a blank line
 * inside ``` is part of the code, not a block boundary.
 */
function splitBlocks(text: string): string[] {
  const lines = text.split("\n");
  const blocks: string[] = [];
  let current: string[] = [];
  let inFence = false;

  const flush = () => {
    if (current.length > 0) {
      blocks.push(current.join("\n"));
      current = [];
    }
  };

  for (const line of lines) {
    if (line.trimStart().startsWith("```")) {
      inFence = !inFence;
      current.push(line);
      continue;
    }
    if (!inFence && line.trim() === "") {
      flush();
      continue;
    }
    current.push(line);
  }
  flush();
  return blocks;
}

export function Markdown({ text, className }: { text: string; className?: string }) {
  const blocks = useMemo(() => splitBlocks(text), [text]);
  return (
    <div className={cn("space-y-3", className)}>
      {blocks.map((block, i) => (
        <MarkdownBlock key={i} source={block} />
      ))}
    </div>
  );
}

/**
 * One top-level block. Memoized on its source string, which is what makes
 * streaming cheap: only the block currently being written re-parses.
 */
const MarkdownBlock = memo(function MarkdownBlock({ source }: { source: string }) {
  return (
    <ReactMarkdown remarkPlugins={[remarkGfm]} components={components}>
      {source}
    </ReactMarkdown>
  );
});

/* ── element styling ─────────────────────────────────────────────────── */

const components: Components = {
  p: ({ children }) => <p className="text-[13.5px] leading-[1.7] break-words">{children}</p>,

  h1: ({ children }) => (
    <h1 className="mt-1 font-display text-base font-semibold tracking-tight">{children}</h1>
  ),
  h2: ({ children }) => (
    <h2 className="mt-1 font-display text-[15px] font-semibold tracking-tight">{children}</h2>
  ),
  h3: ({ children }) => <h3 className="mt-1 text-[13.5px] font-semibold">{children}</h3>,
  h4: ({ children }) => <h4 className="mt-1 text-[13px] font-semibold">{children}</h4>,

  ul: ({ children }) => (
    <ul className="ml-1 space-y-1 text-[13.5px] leading-[1.7] [&>li]:relative [&>li]:pl-4">
      {children}
    </ul>
  ),
  ol: ({ children }) => (
    <ol className="ml-1 list-decimal space-y-1 pl-4 text-[13.5px] leading-[1.7] marker:text-(--color-muted-foreground)">
      {children}
    </ol>
  ),
  li: ({ children, ...props }) => {
    // GFM task list items carry a checkbox child; those keep the default
    // bullet-less layout instead of the custom dot.
    const isTask = "className" in props && String(props.className).includes("task-list-item");
    return (
      <li
        className={cn(
          !isTask &&
            "before:absolute before:left-0 before:text-(--color-muted-foreground) before:content-['·']",
        )}
      >
        {children}
      </li>
    );
  },

  a: ({ children, href }) => (
    <a
      href={href}
      target="_blank"
      rel="noopener noreferrer"
      className="text-(--color-brand-600) underline underline-offset-2 hover:text-(--color-brand-700) dark:text-(--color-brand-400)"
    >
      {children}
    </a>
  ),

  strong: ({ children }) => <strong className="font-semibold">{children}</strong>,
  em: ({ children }) => <em className="italic">{children}</em>,
  del: ({ children }) => <del className="opacity-60">{children}</del>,

  blockquote: ({ children }) => (
    <blockquote className="border-l-2 border-(--color-border) pl-3 text-[13.5px] leading-[1.7] text-(--color-muted-foreground)">
      {children}
    </blockquote>
  ),

  hr: () => <hr className="border-(--color-border)" />,

  // Tables scroll inside their own box so a wide one never widens the page.
  table: ({ children }) => (
    <div className="overflow-x-auto rounded-xl border border-(--color-border)">
      <table className="w-full border-collapse text-[12.5px]">{children}</table>
    </div>
  ),
  thead: ({ children }) => <thead className="bg-(--color-muted)">{children}</thead>,
  th: ({ children }) => (
    <th className="border-b border-(--color-border) px-3 py-1.5 text-left font-medium">
      {children}
    </th>
  ),
  td: ({ children }) => (
    <td className="border-b border-(--color-border) px-3 py-1.5 align-top last:border-b-0">
      {children}
    </td>
  ),

  code: ({ className, children, ...props }) => {
    // react-markdown marks fenced blocks with `language-*`; anything else
    // reaching this component is inline code.
    const match = /language-(\w+)/.exec(className ?? "");
    const text = String(children).replace(/\n$/, "");

    if (!match && !text.includes("\n")) {
      return (
        <code
          className="rounded-md border border-(--color-border) bg-(--color-muted) px-1 py-0.5 font-mono text-[12px]"
          {...props}
        >
          {children}
        </code>
      );
    }
    return <CodeBlock code={text} lang={match?.[1] ?? ""} />;
  },

  // The <pre> wrapper is dropped: CodeBlock renders its own.
  pre: ({ children }) => <>{children}</>,
};

function CodeBlock({ code, lang }: { code: string; lang: string }) {
  const t = useT();
  const { copied, copy } = useCopy(code);

  return (
    <div className="overflow-hidden rounded-xl border border-(--color-border) bg-(--color-muted)">
      <div className="flex items-center justify-between border-b border-(--color-border) px-3 py-1">
        <span className="font-mono text-[10px] uppercase tracking-[0.14em] text-(--color-muted-foreground)">
          {lang || "código"}
        </span>
        <button
          type="button"
          onClick={copy}
          aria-label={t.app.modules.agents.chat.copyCode}
          className="rounded-md p-1 text-(--color-muted-foreground) transition-colors hover:bg-(--color-card) hover:text-(--color-foreground)"
        >
          {copied ? <Check className="size-3" /> : <Copy className="size-3" />}
        </button>
      </div>
      <pre className="overflow-x-auto p-3 text-[12.5px] leading-relaxed">
        <code className="font-mono">{code}</code>
      </pre>
    </div>
  );
}
