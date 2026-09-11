import { useEffect, useRef, useState } from "react";
import type { HTMLAttributes, ReactNode } from "react";
import { cn } from "@/lib/utils";

type RevealProps = Omit<HTMLAttributes<HTMLElement>, "ref"> & {
  delay?: number;
  as?: "div" | "section" | "article" | "li";
  children: ReactNode;
};

/**
 * Scroll-triggered fade + lift. One-shot per element. Cheap.
 */
export function Reveal({
  delay = 0,
  as = "div",
  className,
  children,
  style,
  ...props
}: RevealProps) {
  const ref = useRef<HTMLElement | null>(null);
  const [visible, setVisible] = useState(false);

  useEffect(() => {
    const node = ref.current;
    if (!node) return;

    if (typeof IntersectionObserver === "undefined") {
      const id = window.requestAnimationFrame(() => setVisible(true));
      return () => window.cancelAnimationFrame(id);
    }

    const obs = new IntersectionObserver(
      (entries) => {
        for (const e of entries) {
          if (e.isIntersecting) {
            setVisible(true);
            obs.disconnect();
            break;
          }
        }
      },
      { threshold: 0.12, rootMargin: "0px 0px -40px 0px" },
    );
    obs.observe(node);
    return () => obs.disconnect();
  }, []);

  const Tag = as as "div";

  return (
    <Tag
      ref={ref as React.RefObject<HTMLDivElement | null>}
      data-visible={visible ? "true" : "false"}
      className={cn("reveal", className)}
      style={{ transitionDelay: `${delay}ms`, ...style }}
      {...(props as HTMLAttributes<HTMLDivElement>)}
    >
      {children}
    </Tag>
  );
}
