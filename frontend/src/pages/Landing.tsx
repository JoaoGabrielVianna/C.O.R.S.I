import { Navbar } from "@/components/layout/Navbar";
import { Footer } from "@/components/layout/Footer";
import { Hero } from "@/components/sections/Hero";
import { Manifesto } from "@/components/sections/Manifesto";
import { Modules } from "@/components/sections/Modules";
import { PlatformReveal } from "@/components/sections/PlatformReveal";
import { LabNotes } from "@/components/sections/LabNotes";

/**
 * Landing — public surface of the personal AI lab.
 * Composition: Navbar → Hero → Manifesto → Modules → Platform → Lab Notes → Footer.
 * Auth lives at /login (separate route).
 */
export function LandingPage() {
  return (
    <div className="min-h-dvh bg-(--color-background) text-(--color-foreground)">
      <Navbar />
      <main>
        <Hero />
        <Manifesto />
        <Modules />
        <PlatformReveal />
        <LabNotes />
      </main>
      <Footer />
    </div>
  );
}
