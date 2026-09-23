/**
 * Finance — domain types (v0.0.0).
 *
 * Single-person + family/household personal finance module.
 *
 * Amounts are stored as **integer cents** (so R$ 12,34 → 1234) to avoid the
 * usual float-rounding traps. All `format.ts` helpers and form inputs handle
 * conversion at the edges.
 *
 * The shape maps directly to a future Postgres schema:
 *
 *   people · categories · transactions · credit_cards · fixed_expenses
 *
 * Future ingestion paths (Job Radar-style insertion point):
 *
 *   manual   (today)
 *   whatsapp (planned · AI parser on top of a connected number)
 *   import   (planned · CSV/OFX bank statements)
 *   ai       (reserved · agent-created transactions from chat or other sources)
 */

export type Cents = number;

/* ── People ──────────────────────────────────────────────────────────── */
export type Person = {
  id: string;
  name: string;
  initials: string;
  role: string;          // free-form: "operator", "partner", "child", "shared"
  active: boolean;
  /**
   * Future: when WhatsApp ingestion is live, the normalizer matches an
   * incoming sender to a person via this number.
   */
  whatsappNumber?: string;
};

/* ── Categories ──────────────────────────────────────────────────────── */
export type CategoryType = "income" | "expense";

/**
 * Transaction type — a superset of CategoryType that adds `transfer` for
 * internal account-to-account movements. Transfers do not count as income
 * or expense in any aggregation; every `byType` filter must check for
 * `type === "income"` or `type === "expense"` explicitly.
 */
export type TransactionType = "income" | "expense" | "transfer";

export type Category = {
  id: string;
  name: string;
  type: CategoryType;
  /** Lucide icon name (resolved by `format.ts`). */
  icon: string;
  /** Tailwind colour token base, e.g. `orange` / `emerald` / `sky`. */
  color: string;
  /** Optional monthly budget in cents. */
  budget?: Cents | null;
};

/* ── Transactions ────────────────────────────────────────────────────── */
export type PaymentMethod = "debit" | "credit" | "pix" | "cash" | "transfer";
export type TransactionStatus = "paid" | "pending" | "scheduled";
export type TransactionSource = "manual" | "whatsapp" | "import" | "ai";

export type Transaction = {
  id: string;
  type: TransactionType;
  /** Who paid, received or initiated (transfer). */
  personId: string;
  amount: Cents;
  description: string;
  /** Required for income/expense; omitted for transfer. */
  categoryId?: string;
  paymentMethod: PaymentMethod;
  /**
   * For credit-method transactions, points at a `CreditCard.id`. Reserved
   * for a future `Account` entity (debit-side ledger).
   */
  accountId?: string | null;
  /**
   * For `type === "transfer"`: source / destination account ids. Resolve
   * via `store.allAccountsById` — the lookup spans both real bank accounts
   * (`state.accounts`) and card-as-account derivations from
   * `state.creditCards`. Legacy free-form strings live in
   * `fromAccount` / `toAccount` and are migrated on store read.
   */
  fromAccountId?: string;
  toAccountId?: string;
  /** @deprecated free-form; kept for back-compat reads until migration. */
  fromAccount?: string;
  /** @deprecated free-form; kept for back-compat reads until migration. */
  toAccount?: string;
  date: number;                // epoch ms — the date of the transaction
  status: TransactionStatus;
  source: TransactionSource;
  notes: string;
  /**
   * Person IDs across whom this transaction's cost is split equally. When
   * present and length ≥ 2, every aggregation (per-person totals, charts,
   * person detail) divides `amount` by `splitAmong.length`. Empty/undefined
   * means the full amount belongs to `personId`. Not applicable to transfers.
   */
  splitAmong?: string[];
  /**
   * Foreign key to `PurchasePlan`. Set on every installment row of a
   * multi-month plan; null for one-off purchases.
   */
  planId?: string;
  /** Position within the plan, `1..plan.totalInstallments`. */
  installmentNumber?: number;
  /**
   * Set on every transaction created by a Transfer. Both legs of the
   * pair share the same id. When present, the row should be treated as
   * `type === "transfer"` for display and is server-classified as
   * EXCLUDED in the totals contract (see docs/totals-contract.md §2.1).
   */
  transferPairId?: string;
  /**
   * @deprecated Legacy fields from before `PurchasePlan` existed. Kept on
   * the type so reads of older localStorage data don't choke; new code
   * paths must use `planId` / `installmentNumber`. Migrated on store read.
   */
  purchaseId?: string;
  installment?: { current: number; total: number };
  createdAt: number;
  updatedAt: number;
};

/* ── Accounts ────────────────────────────────────────────────────────── */
/**
 * Minimal account model — exists ONLY to disambiguate transfer endpoints
 * and to give salaries / debits an "lands here" reference. No balance is
 * stored: balances are derived per-render from transactions when needed.
 *
 * Two flavours:
 *   · `bank` — checking accounts the operator maintains (Nubank Conta,
 *     Santander Conta, ...). Real rows in `state.accounts`.
 *   · `card` — credit cards exposed through the same shape so transfer
 *     pickers can target invoice payments. NOT stored separately; derived
 *     on the fly from `state.creditCards` via the store selector
 *     `allAccountsById`. Don't write `type: "card"` rows to state.
 */
export type AccountType = "bank" | "card";

export type Account = {
  id: string;
  name: string;
  ownerId: string;
  type: AccountType;
  /** Lowercase institution slug (e.g., "nubank", "santander"). Display-only. */
  institution?: string;
  deletedAt?: number;
};

/* ── Credit cards ────────────────────────────────────────────────────── */
export type CreditCard = {
  id: string;
  name: string;
  /** Who pays the bill. Required in the multi-person model. */
  ownerId: string;
  limit: Cents;
  closingDay: number;          // 1-31
  dueDay: number;              // 1-31
  brand?: string;              // "visa" / "mastercard" / "amex" — display only
  color?: string;              // Tailwind colour token base
  /** Last 4 digits — optional. Renders in `<CardPreview />`. */
  last4?: string;
  /**
   * Soft-delete timestamp. When set, the card is hidden from pickers and the
   * active list, but historical transactions referencing it still resolve a
   * label through `cardsById`. Hard-delete is intentionally not exposed —
   * orphaning credit transactions corrupts invoice rollups.
   */
  deletedAt?: number;
};

/* ── Fixed expenses ──────────────────────────────────────────────────── */
export type RecurringFrequency = "monthly" | "annual";

/**
 * Money that repeats, in either direction.
 *
 * The direction is NOT here: it comes from the category, exactly as a
 * transaction's `type` does. Resolve it through `categoriesById`.
 */
export type RecurringEntry = {
  id: string;
  description: string;
  amount: Cents;
  categoryId: string;
  personId: string;
  dueDay: number;              // 1-31
  recurrence: RecurringFrequency;
  /**
   * WHICH month an annual obligation falls in, 1..12. Absent on a monthly
   * entry, required on an annual one.
   *
   * It is NOT derived from `startsAt`: that says when the definition began
   * (the day somebody wrote it down), and this says when the money is due.
   * An IPVA recorded in September is still due in January.
   *
   * Undefined on an annual entry means a row written before the column
   * existed. The backend leaves it readable and INERT — it appears in no
   * month — and reports it so a screen can ask for the missing month
   * rather than inventing one.
   */
  dueMonth?: number;
  /**
   * The amount is not the same every time: an electricity bill, a card
   * invoice. `amount` stays required and becomes the EXPECTED figure, and
   * each month's real one is confirmed against the occurrence.
   */
  amountVaries?: boolean;
  status: "active" | "paused";
  /**
   * When the recurrence began. Required for new entries; legacy entries
   * backfill to 0 on read (treated as "always been active").
   */
  startsAt: number;
  /**
   * When the recurrence stopped (epoch ms). Undefined = still recurring.
   * Projection respects `endsAt` and stops projecting future months past it.
   * Cancelling preserves historical records — hard-delete is only used when
   * the user explicitly chose "remove retroactively".
   */
  endsAt?: number;
  notes: string;
};

/* ── Purchase plans ──────────────────────────────────────────────────── */
export type PurchasePlanStatus = "active" | "cancelled" | "completed";

/**
 * Parent record for a multi-installment credit purchase. The N installment
 * transactions are children, linked via `Transaction.planId`. Holds the
 * full commitment amount so audits don't need to sum children (which can
 * drift by N-1 cents from rounding).
 */
export type PurchasePlan = {
  id: string;
  description: string;
  /** Full purchase amount — the bank-locked commitment. */
  originalAmount: Cents;
  totalInstallments: number;
  perInstallment: Cents;
  /** Card-Account id this was charged to (always type=card). */
  accountId: string;
  personId: string;
  categoryId: string;
  purchasedAt: number;
  status: PurchasePlanStatus;
  /**
   * When set, installments above this number have been cancelled (their
   * scheduled transactions removed). Paid installments are preserved.
   */
  cancelledAfterInstallment?: number;
  cancelledAt?: number;
  notes: string;
  createdAt: number;
  updatedAt: number;
};

/* ── Filters (Transactions tab) ──────────────────────────────────────── */
export type FinanceFilters = {
  query: string;
  personIds: string[];
  categoryIds: string[];
  paymentMethods: PaymentMethod[];
  /** YYYY-MM (current month default). */
  month: string;
  type: "all" | "income" | "expense" | "transfer";
};

export type FinanceTab =
  | "overview"
  | "transactions"
  | "cards"
  | "categories"
  | "people"
  | "recurring"
  | "plans"
  | "projection";

export type FinanceState = {
  people: Person[];
  categories: Category[];
  transactions: Transaction[];
  creditCards: CreditCard[];
  recurringEntries: RecurringEntry[];
  /** Bank accounts only. Card accounts are derived from creditCards. */
  accounts: Account[];
  /** Multi-installment credit purchases — parents of installment txs. */
  purchasePlans: PurchasePlan[];
  filters: FinanceFilters;
};

export const EMPTY_FILTERS: FinanceFilters = {
  query: "",
  personIds: [],
  categoryIds: [],
  paymentMethods: [],
  month: currentMonthKey(),
  type: "all",
};

export function currentMonthKey(date = new Date()): string {
  return `${date.getFullYear()}-${String(date.getMonth() + 1).padStart(2, "0")}`;
}
