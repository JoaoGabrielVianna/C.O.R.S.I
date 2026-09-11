/**
 * TanStack Query hooks for the Persons aggregate.
 *
 * Backend stores `name` and `notes`. Frontend extras (`role`, `active`,
 * `whatsappNumber`) live in a local sidecar (see `personMetadata.ts`).
 * `initials` is derived from `name` at codec time.
 *
 * Workspace isolation: same scoping pattern as Cards/Categories.
 *
 * Soft-delete semantics: the backend flips `deleted_at` and historical
 * transactions referencing the person stay intact. The list endpoint
 * filters out soft-deleted rows, but a person still resolves via
 * `peopleById` in transaction rendering for already-loaded data.
 */

import {
  useMutation,
  useQuery,
  useQueryClient,
  type UseMutationResult,
  type UseQueryResult,
} from "@tanstack/react-query";

import { getApiWorkspaceId } from "@/lib/api/workspace";
import {
  createPerson as apiCreate,
  deletePerson as apiDelete,
  getPerson as apiGet,
  listPersons,
  updatePerson as apiUpdate,
  type ApiPerson,
  type CreatePersonRequest,
  type UpdatePersonRequest,
} from "@/modules/finance/api/persons";
import {
  clearPersonMetadata,
  getPersonMetadata,
  setPersonMetadata,
  type PersonMetadata,
} from "@/modules/finance/api/personMetadata";
import { initialsFromName } from "@/pages/app/modules/finance/format";
import type { Person } from "@/pages/app/modules/finance/types";

/* ── codec ──────────────────────────────────────────────────────────── */

function fromApi(api: ApiPerson): Person {
  const md = getPersonMetadata(api.id);
  return {
    id: api.id,
    name: api.name,
    initials: initialsFromName(api.name),
    role: md.role ?? "partner",
    active: md.active !== false,        // default to active
    whatsappNumber: md.whatsappNumber,
  };
}

/* ── keys ───────────────────────────────────────────────────────────── */

export const personsRootKey = (workspaceId: string) =>
  ["finance", "persons", workspaceId] as const;

/* ── query ──────────────────────────────────────────────────────────── */

export function usePersons(): UseQueryResult<Person[]> {
  const workspaceId = getApiWorkspaceId();
  return useQuery({
    queryKey: personsRootKey(workspaceId),
    queryFn: async () => (await listPersons()).map(fromApi),
    staleTime: 30_000,
  });
}

/* ── create ─────────────────────────────────────────────────────────── */

export interface CreatePersonInput {
  name: string;
  notes?: string;
  role?: string;
  active?: boolean;
  whatsappNumber?: string;
}

export function useCreatePerson(): UseMutationResult<
  Person,
  Error,
  CreatePersonInput,
  { workspaceId: string }
> {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async (input) => {
      const body: CreatePersonRequest = {
        name: input.name,
        notes: input.notes ?? "",
      };
      const created = await apiCreate(body);
      const md: PersonMetadata = {
        role: input.role,
        active: input.active,
        whatsappNumber: input.whatsappNumber,
      };
      setPersonMetadata(created.id, md);
      return fromApi(created);
    },
    onSettled: () => {
      const workspaceId = getApiWorkspaceId();
      qc.invalidateQueries({ queryKey: personsRootKey(workspaceId) });
    },
  });
}

/* ── update ─────────────────────────────────────────────────────────── */

export interface UpdatePersonInput {
  id: string;
  name?: string;
  notes?: string;
  role?: string;
  active?: boolean;
  whatsappNumber?: string;
}

export function useUpdatePerson(): UseMutationResult<
  Person,
  Error,
  UpdatePersonInput
> {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async ({ id, name, notes, role, active, whatsappNumber }) => {
      const wireChanged = name !== undefined || notes !== undefined;
      const updated: ApiPerson = wireChanged
        ? await apiUpdate(id, {
            ...(name !== undefined ? { name } : {}),
            ...(notes !== undefined ? { notes } : {}),
          } satisfies UpdatePersonRequest)
        : await apiGet(id);
      const md: PersonMetadata = {};
      if (role !== undefined)           md.role = role;
      if (active !== undefined)         md.active = active;
      if (whatsappNumber !== undefined) md.whatsappNumber = whatsappNumber;
      if (Object.keys(md).length > 0) setPersonMetadata(id, md);
      return fromApi(updated);
    },
    onSettled: () => {
      const workspaceId = getApiWorkspaceId();
      qc.invalidateQueries({ queryKey: personsRootKey(workspaceId) });
    },
  });
}

/* ── delete (soft) ──────────────────────────────────────────────────── */

export function useDeletePerson(): UseMutationResult<void, Error, string> {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async (id) => {
      await apiDelete(id);
      clearPersonMetadata(id);
    },
    onSettled: () => {
      const workspaceId = getApiWorkspaceId();
      qc.invalidateQueries({ queryKey: personsRootKey(workspaceId) });
    },
  });
}
