import request from "./request";

export type MetaModules = {
  domainMonitor?: boolean;
  callback?: boolean;
  wsTicketRequired?: boolean;
  telegramAttendance?: boolean;
};

let cached: MetaModules | null = null;
let inflight: Promise<MetaModules> | null = null;

export function invalidateMetaModulesCache(): void {
  cached = null;
  inflight = null;
}

export async function fetchMetaModules(force = false): Promise<MetaModules> {
  if (!force && cached) {
    return cached;
  }
  if (!force && inflight) {
    return inflight;
  }

  inflight = request
    .get<MetaModules>("/meta/modules")
    .then(({ data }) => {
      cached = data ?? {};
      return cached;
    })
    .catch((err) => {
      cached = null;
      throw err;
    })
    .finally(() => {
      inflight = null;
    });

  return inflight;
}
