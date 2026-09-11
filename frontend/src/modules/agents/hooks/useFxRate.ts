/**
 * Live USD→BRL rate for the usage panel.
 *
 * Cached for hours: the rate moves slowly and the source only publishes
 * daily, so refetching more often would just add noise and load. A failure
 * is not retried into the ground — the panel degrades to USD when the rate
 * is missing.
 */

import { useQuery, type UseQueryResult } from "@tanstack/react-query";

import { getUsdToBrl, type FxRate } from "@/modules/agents/api/fx";

export function useUsdToBrl(): UseQueryResult<FxRate> {
  return useQuery({
    queryKey: ["fx", "usd-brl"],
    queryFn: () => getUsdToBrl(),
    staleTime: 6 * 60 * 60 * 1000, // 6h
    gcTime: 24 * 60 * 60 * 1000,
    refetchOnWindowFocus: false,
    retry: 1,
  });
}
