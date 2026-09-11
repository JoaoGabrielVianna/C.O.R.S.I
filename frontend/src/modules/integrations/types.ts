export type TelegramModuleKey = "finance" | "crm" | "calendar";

export type TelegramConnection = {
  username: string;
  connectedAt: string;
  modules: Record<TelegramModuleKey, boolean>;
};

export type TelegramPairing = {
  code: string;
  expiresAt: number;
  botHandle: string;
  deepLink: string;
};

export type TelegramStatus =
  | { kind: "disconnected" }
  | { kind: "connecting"; pairing: TelegramPairing }
  | { kind: "connected"; connection: TelegramConnection };
