import axios from "axios";
const request = axios.create({
  baseURL: "/api/v1",
  timeout: 90000,
  withCredentials: true,
  headers: { "X-Requested-With": "TGAttendance" },
});
function camel(v: any): any {
  if (Array.isArray(v)) return v.map(camel);
  if (v && typeof v === "object" && !(v instanceof Blob)) {
    return Object.fromEntries(
      Object.entries(v).map(([k, x]) => [
        k.replace(/_([a-z])/g, (_, s) => s.toUpperCase()),
        camel(x),
      ]),
    );
  }
  return v;
}
function syncPermissions(headers:any){
 const raw=headers?.['x-tg-permissions'];if(!raw)return;
 try{const permissions=JSON.parse(raw);if(Array.isArray(permissions))window.dispatchEvent(new CustomEvent('tg-permissions',{detail:permissions}))}catch{}
}
request.interceptors.response.use(
  (response) => {
    syncPermissions(response.headers);
    if (response.config.responseType === "blob") return response;
    const res = camel(response.data);
    if (res.code && res.code !== 0)
      return Promise.reject(new Error(res.message || "请求失败"));
    return res;
  },
  (error) => {
    syncPermissions(error.response?.headers);
    if (
      error.response?.status === 401 &&
      !location.pathname.startsWith("/login")
    )
      location.assign("/login");
    return Promise.reject(
      new Error(
        error.response?.data?.message ||
          "请求失败，请检查连接；免费服务唤醒可能需要一分钟。",
      ),
    );
  },
);
export default request;
export { request };
