# Totals Contract

**Status:** FROZEN — 2026-05-25
**Owner:** finance bounded context
**Scope:** every dashboard total, every aggregation endpoint, every report
that quotes "income," "expense," "available," or "projected" for a
workspace.

> This document is the canonical source of finance totals semantics.
> Every consumer — **Transfers**, **PurchasePlans**, recurring entries,
> shared expenses and the dashboard read models — **consumes these
> definitions; none of them redefines them**. Any change to this contract requires an explicit
> unfreeze decision from the contract owner and a coordinated rollout
> across all consumers. No silent extensions.

---

## 1. Definitions

Every finance-domain event that contributes to a workspace ledger maps
to exactly one of three buckets:

| Bucket        | Meaning                                                                                                  |
| ------------- | -------------------------------------------------------------------------------------------------------- |
| **REALIZED**  | Money that has actually moved. Already affects the workspace's true balance and historical totals.       |
| **PROJECTED** | Money expected to move. Affects the workspace's *forward-looking* view (cashflow, runway, budget burn).  |
| **EXCLUDED**  | An event that moves money but does **not** count as income or expense for the workspace as a whole.      |

Buckets are **disjoint and exhaustive**. A row is in exactly one bucket
at any given moment. The classification is a pure function of the row
plus the current wall clock — no per-consumer overrides.

---

## 2. Classification rules

The rules below evaluate **top-down**: the first matching rule wins.
This ordering matters — `EXCLUDED` is checked before status/date so a
transfer is never accidentally counted, even if its status is `paid`.

### 2.1 EXCLUDED — checked first

A row is excluded from both income and expense totals when **any** of
the following is true:

- The row represents a **Transfer** between accounts owned by the same
  workspace. Transfers move money internally; counting them as income
  *or* expense would double-count the workspace's own funds.
  - In v0.1, transfers do not exist as a first-class entity. When the
    Transfer entity lands, **rows tagged as transfers must be excluded
    regardless of `payment_method`, `status`, or any other field.**
  - The current `payment_method = 'transfer'` enum value is a payment-
    instrument tag, **not** a transfer marker. Do **not** use
    `payment_method` to detect transfers. See §5.
- The row is soft-deleted (`deleted_at IS NOT NULL`). Already filtered
  by repos but called out here for completeness.

### 2.2 REALIZED

A row is realized when **all** of the following hold:

- Not excluded by §2.1, **and**
- `status = 'paid'`, **and**
- `occurred_at <= now()` (where `now()` is the server clock at query
  time, in UTC).

A backdated `paid` row (one whose `occurred_at` is in the past but was
recorded today) counts as realized — money moved, the system simply
learned about it late.

### 2.3 PROJECTED

A row is projected when it is **not excluded** and **not realized** —
i.e., one of the following:

- `status = 'pending'`, **or**
- `status = 'scheduled'`, **or**
- `status = 'paid' AND occurred_at > now()` (a future-dated paid row).

A future-dated `paid` row stays projected until the clock catches up.
The transition realized↔projected is implicit and time-driven; no
write is required.

### 2.4 Decision table

| `status`    | `occurred_at` vs `now` | excluded by §2.1? | Bucket    |
| ----------- | ---------------------- | ----------------- | --------- |
| paid        | ≤ now                  | no                | REALIZED  |
| paid        | > now                  | no                | PROJECTED |
| pending     | any                    | no                | PROJECTED |
| scheduled   | any                    | no                | PROJECTED |
| *any*       | any                    | **yes**           | EXCLUDED  |

---

## 3. PurchasePlans

A PurchasePlan represents a single purchase paid across N installments
(e.g., a 12× credit-card charge). PurchasePlans are **not** a separate
ledger — they are a parent record that materializes one Transaction per
installment.

The contract for installments is a strict corollary of §2:

- **Future installments** (status `scheduled`, or `paid` with
  `occurred_at > now`) → **PROJECTED**.
- **Paid installments** (status `paid` AND `occurred_at <= now`) →
  **REALIZED**.
- A PurchasePlan in aggregate is **never** classified on its own. Totals
  always sum the underlying installment rows. There is no plan-level
  shortcut that lets a consumer count the full purchase price at
  purchase time.

This means a 12× R$ 1.200 plan opened today contributes:

- Month 0 installment paid today → R$ 100 realized expense.
- Months 1–11 scheduled → R$ 1.100 projected expense.
- Total *realized + projected* equals the full R$ 1.200 — never more,
  never less.

PurchasePlans **must not** introduce a new bucket, a new status, or a
new exclusion rule. If the future entity needs richer metadata (e.g.,
"merchant," "installment_index," "plan_id"), those are display/filter
attributes — they do not change which bucket a row lands in.

---

## 4. Examples

Walked through to remove ambiguity. `now = 2026-05-25T12:00Z` in all
examples.

| # | Scenario                                       | `type`  | `status`  | `occurred_at`     | Tagged transfer? | Bucket    |
|---|------------------------------------------------|---------|-----------|-------------------|------------------|-----------|
| 1 | Salary credited yesterday                      | income  | paid      | 2026-05-24        | no               | REALIZED  |
| 2 | Salary expected next month                     | income  | scheduled | 2026-06-25        | no               | PROJECTED |
| 3 | Credit-card bill auto-scheduled for due date   | expense | scheduled | 2026-06-10        | no               | PROJECTED |
| 4 | Credit-card bill paid today                    | expense | paid      | 2026-05-25T10:00Z | no               | REALIZED  |
| 5 | Pix sent, awaiting confirmation                | expense | pending   | 2026-05-25T11:59Z | no               | PROJECTED |
| 6 | Transfer from checking → savings (own account) | —       | paid      | 2026-05-25        | **yes**          | EXCLUDED  |
| 7 | 12× purchase opened today, installment 0       | expense | paid      | 2026-05-25        | no               | REALIZED  |
| 8 | Same purchase, installments 1–11               | expense | scheduled | 2026-06-25 … 2027-04-25 | no         | PROJECTED |
| 9 | Backdated paid expense (logged today)          | expense | paid      | 2026-05-20        | no               | REALIZED  |
|10 | Future-dated paid row (rare; manual entry)     | expense | paid      | 2026-06-01        | no               | PROJECTED |

---

## 5. Invariants

These hold for every workspace, every window, every consumer:

1. **Disjoint + exhaustive.** Every non-deleted finance row belongs to
   exactly one of {REALIZED, PROJECTED, EXCLUDED}.
2. **Conservatism.** When the classification is uncertain, the
   contract prefers **PROJECTED** over **REALIZED**. The dashboard must
   never overstate available money. Under-counting is acceptable;
   over-counting is a defect.
3. **No double counting from transfers.** The sum of REALIZED income
   minus REALIZED expense across the entire history of a workspace
   equals the workspace's true net cash movement — never inflated by
   internal transfers.
4. **No double counting from plans.** The sum of (REALIZED + PROJECTED)
   expense from a PurchasePlan equals the plan's principal. Never
   `principal × 2`, never `principal × N`.
5. **No surprise reclassification.** A row's bucket can change only
   because (a) its `status` changed, (b) its `occurred_at` changed,
   (c) it was tagged/untagged as a transfer, (d) it was soft-deleted/
   restored, or (e) the wall clock crossed its `occurred_at`. No
   consumer is allowed to apply additional bucketing logic.
6. **`payment_method` is presentation, not classification.** The
   `payment_method = 'transfer'` enum value does **not** make a row
   excluded. Only an explicit Transfer tag (defined when the entity
   lands) does.
7. **Soft-deleted rows are invisible.** They contribute to no bucket.

---

## 6. Cross-references

Authoritative consumers as of the freeze date:

- **OpenAPI:** [api/openapi/finance.yaml](../api/openapi/finance.yaml)
  — `GET /finance/transactions/totals` (`TransactionTotals` /
  `TotalsBucket` schemas). Today's response groups by `status`,
  `category`, `method` only; the v-next response must surface
  `realized` / `projected` / `excluded` buckets per the rules above
  without changing the meaning of the existing keys.
- **HTTP handler:** [internal/finance/adapters/httpapi/transactions.go](../internal/finance/adapters/httpapi/transactions.go)
  — `Handler.getTransactionTotals`.
- **Application service:** [internal/finance/app/transactions.go](../internal/finance/app/transactions.go)
  — `Service.GetTransactionTotals`.
- **Repository:** [internal/finance/adapters/repo/transactions.go](../internal/finance/adapters/repo/transactions.go)
  — `TransactionRepo.Totals` (per-status, per-category, per-method
  aggregation SQL).
- **Ports:** [internal/finance/ports/ports.go](../internal/finance/ports/ports.go)
  — `TransactionTotals`, `TotalsBucket`, `CategoryTotal`,
  `MethodTotal`.
- **Tests:** [internal/finance/finance_integration_test.go](../internal/finance/finance_integration_test.go)
  — `TestTransactionTotals_MatchesGroundTruth`,
  `TestTransactionTotals_RejectsMissingWindow`. New tests for
  realized/projected/excluded must be added alongside these and must
  cover at minimum: the §2.4 decision table, transfer exclusion, and
  the plan-installment split.

---

## 7. Change procedure

This contract is frozen. To change it:

1. Open a proposal that names the rule being changed and the consumer
   impact (handlers, dashboard, reports, exports).
2. Get explicit owner approval to unfreeze.
3. Update this document, then every consumer in §6 in the **same**
   change set. Partial rollouts are forbidden — the contract is what
   keeps consumers coherent.
4. Add migration notes covering rows whose bucket would shift under
   the new rules.

Until that procedure is followed, any work on Transfers, PurchasePlans,
recurring entries, shared expenses, dashboards, or a new aggregation
endpoint **must** treat §§1–5 as immutable.
