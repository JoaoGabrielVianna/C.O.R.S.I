import type { TelegramConnection, TelegramPairing } from "../types";

const STORAGE_KEY = "corsi.integrations.telegram.v1";
const PAIRING_TTL_MS = 5 * 60 * 1000;
const BOT_HANDLE = "@CorsiSecretarioBot";
const BOT_DEEPLINK = "https://t.me/CorsiSecretarioBot";

type Persisted =
  | { kind: "disconnected" }
  | { kind: "connecting"; pairing: TelegramPairing }
  | { kind: "connected"; connection: TelegramConnection };

function read(): Persisted {
  if (typeof window === "undefined") return { kind: "disconnected" };
  try {
    const raw = window.localStorage.getItem(STORAGE_KEY);
    if (!raw) return { kind: "disconnected" };
    const parsed = JSON.parse(raw) as Persisted;
    if (parsed.kind === "connecting" && parsed.pairing.expiresAt < Date.now()) {
      return { kind: "disconnected" };
    }
    return parsed;
  } catch {
    return { kind: "disconnected" };
  }
}

function write(value: Persisted) {
  if (typeof window === "undefined") return;
  window.localStorage.setItem(STORAGE_KEY, JSON.stringify(value));
}

function randomCode(): string {
  const alphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789";
  let out = "";
  for (let i = 0; i < 6; i += 1) {
    out += alphabet[Math.floor(Math.random() * alphabet.length)];
  }
  return `CORSI-${out}`;
}

function delay<T>(value: T, ms = 220): Promise<T> {
  return new Promise((resolve) => setTimeout(() => resolve(value), ms));
}

export const telegramApi = {
  async status() {
    return delay(read());
  },

  async generateCode(): Promise<TelegramPairing> {
    const code = randomCode();
    const pairing: TelegramPairing = {
      code,
      expiresAt: Date.now() + PAIRING_TTL_MS,
      botHandle: BOT_HANDLE,
      deepLink: `${BOT_DEEPLINK}?start=${code}`,
    };
    write({ kind: "connecting", pairing });
    return delay(pairing);
  },

  async cancelPairing() {
    write({ kind: "disconnected" });
    return delay(undefined);
  },

  async simulateConfirmation(): Promise<TelegramConnection> {
    const connection: TelegramConnection = {
      username: "joao_corsi",
      connectedAt: new Date().toISOString(),
      modules: { finance: true, crm: false, calendar: false },
    };
    write({ kind: "connected", connection });
    return delay(connection);
  },

  async setModule(key: keyof TelegramConnection["modules"], on: boolean) {
    const current = read();
    if (current.kind !== "connected") return delay(null);
    const next: TelegramConnection = {
      ...current.connection,
      modules: { ...current.connection.modules, [key]: on },
    };
    write({ kind: "connected", connection: next });
    return delay(next);
  },

  async disconnect() {
    write({ kind: "disconnected" });
    return delay(undefined);
  },

  async test(): Promise<{ ok: true; message: string }> {
    return new Promise((resolve) => {
      setTimeout(() => resolve({ ok: true, message: "Conexão ativa" }), 900);
    });
  },
};
