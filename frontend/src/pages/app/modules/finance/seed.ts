import type {
  Category,
  CreditCard,
  RecurringEntry,
  Person,
  Transaction,
} from "./types";
import { initialsFromName } from "./format";

/**
 * Mock seed for the Finance module (v0.0.0).
 *
 * Fictional figures and people. Nothing here describes a real budget.
 * Preview-tagged via `source: "manual"` and
 * the "Frontend preview" badge in the UI. When the collector/normalizer ships,
 * drop the seed flag and let real data take over.
 *
 * BRL amounts are stored as integer cents.
 */

export const SEED_FLAG_KEY = "corsi.module.finance.seed.v1";

function id(prefix: string, key: string): string {
  return `${prefix}_${key}`;
}

function makeRunId(prefix: string): string {
  return `${prefix}_${Math.random().toString(36).slice(2, 8)}`;
}

/* ── People ─────────────────────────────────────────────────────────── */
const PEOPLE: ReadonlyArray<Omit<Person, "initials">> = [
  { id: id("p", "owner"),   name: "Alex Demo",  role: "operator", active: true },
  { id: id("p", "partner"), name: "Robin Demo", role: "partner",  active: true },
];

/* ── Categories ──────────────────────────────────────────────────────── */
const CATEGORIES: ReadonlyArray<Category> = [
  // Income
  { id: id("c", "salary"),      name: "Salário",       type: "income",  icon: "briefcase", color: "emerald" },
  { id: id("c", "freelance"),   name: "Freelance",     type: "income",  icon: "code",      color: "teal" },
  { id: id("c", "investments"), name: "Investimentos", type: "income",  icon: "trending",  color: "sky" },
  // Expense
  { id: id("c", "food"),       name: "Alimentação",   type: "expense", icon: "utensils",   color: "orange" as string, budget: 180_000 },
  { id: id("c", "housing"),    name: "Moradia",       type: "expense", icon: "home",       color: "slate",  budget: 450_000 },
  { id: id("c", "transport"),  name: "Transporte",    type: "expense", icon: "car",        color: "amber",  budget: 80_000 },
  { id: id("c", "health"),     name: "Saúde",         type: "expense", icon: "heart",      color: "rose",   budget: 100_000 },
  { id: id("c", "leisure"),    name: "Lazer",         type: "expense", icon: "gamepad",    color: "fuchsia", budget: 50_000 },
  { id: id("c", "education"),  name: "Educação",      type: "expense", icon: "book",       color: "indigo" },
  { id: id("c", "subs"),       name: "Assinaturas",   type: "expense", icon: "sparkles",   color: "violet", budget: 50_000 },
  { id: id("c", "shopping"),   name: "Compras",       type: "expense", icon: "shopping",   color: "purple" },
  { id: id("c", "pets"),       name: "Pets",          type: "expense", icon: "pet",        color: "pink" },
  { id: id("c", "utilities"),  name: "Contas",        type: "expense", icon: "zap",        color: "yellow", budget: 60_000 },
];

/* Note: "orange" isn't in the safelist; mapping it to "amber" in `AVAILABLE_COLORS`. */
// Replace any "orange" → "amber" at runtime to keep classnames in the safelist.
function normalizeColor(c: string): string {
  if (c === "orange") return "amber";
  return c;
}

/* ── Credit cards ───────────────────────────────────────────────────── */
const CARDS: ReadonlyArray<CreditCard> = [
  {
    id: id("cc", "nubank"),
    name: "Nubank · Visa Black",
    ownerId: id("p", "owner"),
    limit: 1_500_000,         // R$ 15.000
    closingDay: 22,
    dueDay: 1,
    brand: "visa",
    color: "violet",
  },
  {
    id: id("cc", "itau"),
    name: "Itaú · Personnalité",
    ownerId: id("p", "partner"),
    limit: 2_500_000,         // R$ 25.000
    closingDay: 15,
    dueDay: 25,
    brand: "mastercard",
    color: "sky",
  },
];

/* ── Fixed expenses ─────────────────────────────────────────────────── */
const FIXED: ReadonlyArray<RecurringEntry> = [
  { id: id("fx", "rent"),     description: "Aluguel",            amount: 240_000, categoryId: id("c", "housing"),   personId: id("p", "owner"), dueDay: 5,  recurrence: "monthly", status: "active", startsAt: 0, notes: "" },
  { id: id("fx", "internet"), description: "Vivo Fibra 700 mb",  amount: 11_990,  categoryId: id("c", "utilities"), personId: id("p", "partner"), dueDay: 10, recurrence: "monthly", status: "active", startsAt: 0, notes: "" },
  { id: id("fx", "spotify"),  description: "Spotify Family",     amount: 3_490,   categoryId: id("c", "subs"),      personId: id("p", "owner"), dueDay: 12, recurrence: "monthly", status: "active", startsAt: 0, notes: "" },
  { id: id("fx", "designtool"),   description: "Ferramenta de design",amount: 5_990,   categoryId: id("c", "subs"),      personId: id("p", "owner"), dueDay: 18, recurrence: "monthly", status: "active", startsAt: 0, notes: "" },
  { id: id("fx", "health"),   description: "Plano de saúde",     amount: 46_000,  categoryId: id("c", "health"),    personId: id("p", "owner"), dueDay: 7,  recurrence: "monthly", status: "active", startsAt: 0, notes: "" },
  { id: id("fx", "gym"),      description: "Smart Fit",          amount: 9_990 ,  categoryId: id("c", "health"),    personId: id("p", "owner"), dueDay: 9,  recurrence: "monthly", status: "active", startsAt: 0, notes: "" },
  { id: id("fx", "icloud"),   description: "iCloud 2 TB",        amount: 4_990,   categoryId: id("c", "subs"),      personId: id("p", "partner"), dueDay: 20, recurrence: "monthly", status: "active", startsAt: 0, notes: "" },
  { id: id("fx", "netflix"),  description: "Netflix Premium",    amount: 5_590,   categoryId: id("c", "subs"),      personId: id("p", "owner"), dueDay: 22, recurrence: "monthly", status: "active", startsAt: 0, notes: "" },
];

/* ── Transactions (recent month) ─────────────────────────────────────── */

type SeedTransaction = Omit<Transaction, "id" | "createdAt" | "updatedAt">;

function daysAgo(now: number, n: number): number {
  const d = new Date(now);
  d.setDate(d.getDate() - n);
  return d.getTime();
}

function daysAhead(now: number, n: number): number {
  const d = new Date(now);
  d.setDate(d.getDate() + n);
  return d.getTime();
}

function buildTransactions(now: number): SeedTransaction[] {
  const owner   = id("p", "owner");
  const partner = id("p", "partner");

  return [
    // Income
    { type: "income",  personId: owner, amount: 850_000  , description: "Salário",                         categoryId: id("c", "salary"),      paymentMethod: "transfer", date: daysAgo(now, 12), status: "paid",      source: "manual", notes: "" },
    { type: "income",  personId: partner, amount: 210_000,   description: "Freelance design",                categoryId: id("c", "freelance"),   paymentMethod: "pix",      date: daysAgo(now, 9),  status: "paid",      source: "manual", notes: "" },
    { type: "income",  personId: owner, amount: 32_000,    description: "Dividendos",                     categoryId: id("c", "investments"), paymentMethod: "transfer", date: daysAgo(now, 5),  status: "paid",      source: "manual", notes: "" },

    // Expense · housing (fixed-style)
    { type: "expense", personId: owner, amount: 240_000, description: "Aluguel maio",                     categoryId: id("c", "housing"),   paymentMethod: "transfer", date: daysAgo(now, 14), status: "paid",      source: "manual", notes: "" },
    { type: "expense", personId: partner, amount: 11_990,  description: "Vivo Fibra",                       categoryId: id("c", "utilities"), paymentMethod: "debit",    date: daysAgo(now, 8),  status: "paid",      source: "manual", notes: "" },
    { type: "expense", personId: owner, amount: 22_400,  description: "Enel · conta de luz",              categoryId: id("c", "utilities"), paymentMethod: "debit",    date: daysAgo(now, 3),  status: "paid",      source: "manual", notes: "" },

    // Expense · food
    { type: "expense", personId: owner, amount: 5_280,  description: "Pão de Açúcar",                     categoryId: id("c", "food"),   paymentMethod: "credit", accountId: id("cc", "nubank"), date: daysAgo(now, 11), status: "paid", source: "manual", notes: "" },
    { type: "expense", personId: partner, amount: 8_790,  description: "St Marche",                         categoryId: id("c", "food"),   paymentMethod: "credit", accountId: id("cc", "itau"),  date: daysAgo(now, 9),  status: "paid", source: "manual", notes: "" },
    { type: "expense", personId: owner, amount: 3_220,  description: "iFood · jantar terça",              categoryId: id("c", "food"),   paymentMethod: "credit", accountId: id("cc", "nubank"), date: daysAgo(now, 7),  status: "paid", source: "manual", notes: "" },
    { type: "expense", personId: partner, amount: 2_490,  description: "Padaria",                           categoryId: id("c", "food"),   paymentMethod: "pix",                                  date: daysAgo(now, 4),  status: "paid", source: "manual", notes: "" },
    { type: "expense", personId: owner, amount: 7_650,  description: "Mercado · feira da semana",         categoryId: id("c", "food"),   paymentMethod: "credit", accountId: id("cc", "nubank"), date: daysAgo(now, 2),  status: "paid", source: "manual", notes: "" },

    // Expense · transport
    { type: "expense", personId: owner, amount: 4_800,  description: "Uber · semana",                     categoryId: id("c", "transport"), paymentMethod: "credit", accountId: id("cc", "nubank"), date: daysAgo(now, 10), status: "paid", source: "manual", notes: "" },
    { type: "expense", personId: partner, amount: 1_290,  description: "99 · ida shopping",                 categoryId: id("c", "transport"), paymentMethod: "pix",                                  date: daysAgo(now, 6),  status: "paid", source: "manual", notes: "" },

    // Expense · leisure
    { type: "expense", personId: owner, amount: 18_900, description: "Show · Pavilhão",                   categoryId: id("c", "leisure"),  paymentMethod: "credit", accountId: id("cc", "itau"),  date: daysAgo(now, 13), status: "paid", source: "manual", notes: "" },
    { type: "expense", personId: partner, amount: 8_500,  description: "Cinema + jantar",                   categoryId: id("c", "leisure"),  paymentMethod: "credit", accountId: id("cc", "nubank"), date: daysAgo(now, 8),  status: "paid", source: "manual", notes: "" },

    // Expense · subs
    { type: "expense", personId: owner, amount: 3_490,  description: "Spotify Family",                    categoryId: id("c", "subs"),     paymentMethod: "credit", accountId: id("cc", "itau"),  date: daysAgo(now, 17), status: "paid", source: "manual", notes: "" },
    { type: "expense", personId: owner, amount: 5_990,  description: "Ferramenta de design",               categoryId: id("c", "subs"),     paymentMethod: "credit", accountId: id("cc", "itau"),  date: daysAgo(now, 11), status: "paid", source: "manual", notes: "" },
    { type: "expense", personId: partner, amount: 4_990,  description: "iCloud 2 TB",                       categoryId: id("c", "subs"),     paymentMethod: "credit", accountId: id("cc", "nubank"), date: daysAgo(now, 15), status: "paid", source: "manual", notes: "" },

    // Expense · pets
    { type: "expense", personId: partner, amount: 14_700, description: "Petlove · ração",                   categoryId: id("c", "pets"),     paymentMethod: "credit", accountId: id("cc", "nubank"), date: daysAgo(now, 6),  status: "paid", source: "manual", notes: "" },

    // Expense · shopping
    { type: "expense", personId: owner, amount: 32_000, description: "Camisa & calça",                    categoryId: id("c", "shopping"), paymentMethod: "credit", accountId: id("cc", "itau"),  date: daysAgo(now, 18), status: "paid", source: "manual", notes: "" },
    { type: "expense", personId: partner, amount: 9_990 , description: "Skincare",                          categoryId: id("c", "shopping"), paymentMethod: "credit", accountId: id("cc", "nubank"), date: daysAgo(now, 10), status: "paid", source: "manual", notes: "" },

    // Scheduled (upcoming)
    { type: "expense", personId: owner, amount: 46_000, description: "Plano de saúde · junho",            categoryId: id("c", "health"),   paymentMethod: "debit",                                date: daysAhead(now, 4), status: "scheduled", source: "manual", notes: "" },
    { type: "expense", personId: owner, amount: 9_990 , description: "Smart Fit",                         categoryId: id("c", "health"),   paymentMethod: "debit",                                date: daysAhead(now, 2), status: "scheduled", source: "manual", notes: "" },
    { type: "expense", personId: partner, amount: 28_000, description: "Curso UX",                          categoryId: id("c", "education"),paymentMethod: "credit", accountId: id("cc", "itau"),  date: daysAhead(now, 7), status: "scheduled", source: "manual", notes: "" },
  ];
}

/* ── Builder ────────────────────────────────────────────────────────── */

export function buildSeed(now: number): {
  people: Person[];
  categories: Category[];
  transactions: Transaction[];
  creditCards: CreditCard[];
  recurringEntries: RecurringEntry[];
} {
  const people: Person[] = PEOPLE.map((p) => ({ ...p, initials: initialsFromName(p.name) }));
  const categories: Category[] = CATEGORIES.map((c) => ({ ...c, color: normalizeColor(c.color) }));
  const creditCards: CreditCard[] = CARDS.map((c) => ({ ...c, color: normalizeColor(c.color ?? "slate") }));
  const recurringEntries: RecurringEntry[] = [...FIXED];
  const transactions: Transaction[] = buildTransactions(now).map((t) => ({
    ...t,
    id: makeRunId("tx"),
    createdAt: t.date,
    updatedAt: t.date,
  }));
  return { people, categories, transactions, creditCards, recurringEntries };
}
