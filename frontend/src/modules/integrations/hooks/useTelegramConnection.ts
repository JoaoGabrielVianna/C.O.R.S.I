import { useCallback, useEffect, useRef, useState } from "react";
import { telegramApi } from "../api/telegram";
import type { TelegramStatus } from "../types";

type TestResult = { state: "idle" | "running" | "ok" | "fail"; message?: string };

export function useTelegramConnection() {
  const [status, setStatus] = useState<TelegramStatus>({ kind: "disconnected" });
  const [loading, setLoading] = useState(true);
  const [busy, setBusy] = useState<"connect" | "cancel" | "disconnect" | "simulate" | null>(null);
  const [test, setTest] = useState<TestResult>({ state: "idle" });
  const [now, setNow] = useState(() => Date.now());
  const timer = useRef<number | null>(null);

  useEffect(() => {
    let cancelled = false;
    telegramApi.status().then((s) => {
      if (!cancelled) {
        setStatus(s);
        setLoading(false);
      }
    });
    return () => {
      cancelled = true;
    };
  }, []);

  useEffect(() => {
    if (status.kind !== "connecting") {
      if (timer.current) {
        window.clearInterval(timer.current);
        timer.current = null;
      }
      return;
    }
    const expiresAt = status.pairing.expiresAt;
    timer.current = window.setInterval(() => {
      const tick = Date.now();
      setNow(tick);
      if (tick >= expiresAt) {
        telegramApi.cancelPairing();
        setStatus({ kind: "disconnected" });
      }
    }, 1000);
    return () => {
      if (timer.current) {
        window.clearInterval(timer.current);
        timer.current = null;
      }
    };
  }, [status]);

  const connect = useCallback(async () => {
    setBusy("connect");
    const pairing = await telegramApi.generateCode();
    setStatus({ kind: "connecting", pairing });
    setNow(Date.now());
    setBusy(null);
  }, []);

  const cancel = useCallback(async () => {
    setBusy("cancel");
    await telegramApi.cancelPairing();
    setStatus({ kind: "disconnected" });
    setBusy(null);
  }, []);

  const simulate = useCallback(async () => {
    setBusy("simulate");
    const connection = await telegramApi.simulateConfirmation();
    setStatus({ kind: "connected", connection });
    setBusy(null);
  }, []);

  const disconnect = useCallback(async () => {
    setBusy("disconnect");
    await telegramApi.disconnect();
    setStatus({ kind: "disconnected" });
    setTest({ state: "idle" });
    setBusy(null);
  }, []);

  const toggleModule = useCallback(
    async (key: "finance" | "crm" | "calendar", on: boolean) => {
      if (status.kind !== "connected") return;
      const optimistic = {
        ...status.connection,
        modules: { ...status.connection.modules, [key]: on },
      };
      setStatus({ kind: "connected", connection: optimistic });
      await telegramApi.setModule(key, on);
    },
    [status],
  );

  const runTest = useCallback(async () => {
    setTest({ state: "running" });
    try {
      const r = await telegramApi.test();
      setTest({ state: "ok", message: r.message });
    } catch {
      setTest({ state: "fail" });
    }
  }, []);

  const remainingMs =
    status.kind === "connecting" ? Math.max(0, status.pairing.expiresAt - now) : 0;

  return {
    status,
    loading,
    busy,
    test,
    remainingMs,
    actions: { connect, cancel, simulate, disconnect, toggleModule, runTest },
  };
}
