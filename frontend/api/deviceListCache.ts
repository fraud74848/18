import type { Device, DeviceListResponse } from "./types";

const DEFAULT_TTL_MS = 30_000;

type ListAllParams = Omit<
  {
    page?: number;
    pageSize?: number;
    status?: string;
    employeeSearch?: string;
    deviceStatus?: string;
    keyword?: string;
  },
  "page" | "pageSize"
>;

type ListAllOptions = {
  force?: boolean;
  ttlMs?: number;
};

type CacheEntry = {
  key: string;
  items: Device[];
  fetchedAt: number;
};

let cache: CacheEntry | null = null;
let inflight: Promise<DeviceListResponse> | null = null;

function cacheKey(params?: ListAllParams): string {
  return JSON.stringify(params ?? {});
}

function cloneDevices(items: Device[]): Device[] {
  return items.map((device) => ({ ...device }));
}

function buildResponse(items: Device[]): { data: DeviceListResponse } {
  const cloned = cloneDevices(items);
  return {
    data: {
      items: cloned,
      total: cloned.length,
      page: 1,
      pageSize: cloned.length,
    },
  };
}

export function invalidateDeviceListCache(): void {
  cache = null;
  inflight = null;
}

/** Paginated fetch — passed in from deviceApi.listAll implementation */
export async function fetchAllDevicesCached(
  fetchAll: (params?: ListAllParams) => Promise<{ data: DeviceListResponse }>,
  params?: ListAllParams,
  options?: ListAllOptions,
): Promise<{ data: DeviceListResponse }> {
  const force = options?.force === true;
  const ttlMs = options?.ttlMs ?? DEFAULT_TTL_MS;
  const key = cacheKey(params);

  if (!force && inflight) {
    const data = await inflight;
    return buildResponse(data.items || []);
  }

  if (
    !force &&
    cache &&
    cache.key === key &&
    Date.now() - cache.fetchedAt < ttlMs
  ) {
    return buildResponse(cache.items);
  }

  inflight = fetchAll(params).then((res) => {
    const items = res.data.items || [];
    cache = { key, items, fetchedAt: Date.now() };
    return res.data;
  });

  try {
    const data = await inflight;
    return buildResponse(data.items || []);
  } finally {
    inflight = null;
  }
}
