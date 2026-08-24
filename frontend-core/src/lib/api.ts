// Cliente mínimo contra backend-core. Todas las llamadas van con
// credentials: "include" porque la sesión local vive en una cookie
// HttpOnly (asterion_local_session) — no hay tokens que manejar acá.
async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const response = await fetch(`/api${path}`, {
    ...init,
    credentials: "include",
    headers: { "Content-Type": "application/json", ...init?.headers },
  });
  if (!response.ok) {
    const body = await response.json().catch(() => ({}));
    throw new Error(body.detail || `Error ${response.status}`);
  }
  return response.json();
}

export interface AuthStatus {
  configured: boolean;
  created_at?: string;
}

export interface MachineInfo {
  hostname: string;
  os: string;
  architecture: string;
  kernel_version?: string;
  cpu_model?: string;
  cpu_cores: number;
  ram_total_gb: number;
  disk_total_gb: number;
  virtualization: string;
}

export interface Snapshot {
  cpu_percent: number;
  ram_used_gb: number;
  ram_total_gb: number;
  disk_used_gb: number;
  disk_total_gb: number;
  network_in_gb: number;
  network_out_gb: number;
  taken_at: number;
}

export interface CostEstimate {
  cpu_cost_hour: number;
  ram_cost_hour: number;
  storage_cost_month: number;
  admin_cost_month: number;
  estimated_monthly_total: number;
  price_source: string;
  prices_updated_at: number | null;
}

export interface RuntimeEnvironment {
  system: MachineInfo;
  service_manager: string;
  privileges: string;
  firewall: string[];
  reverse_proxy: string[];
  tunnel: string[];
  tls: string;
  discovered_at: string;
}

export interface RuntimeConfig {
  runtime_name: string;
  service_bind: string;
  service_port: number;
  metrics_enabled: boolean;
  metrics_interval_seconds: number;
  heartbeat_enabled: boolean;
  heartbeat_interval_seconds: number;
  remote_management_enabled: boolean;
}

export interface RuntimeStatus {
  environment: RuntimeEnvironment;
  config: RuntimeConfig;
}

export interface DoctorCheck {
  name: string;
  pass: boolean | null;
  detail: string;
}

export interface DoctorReport {
  runtime: DoctorCheck[];
  security: DoctorCheck[];
  reverse_proxy: DoctorCheck[];
  tunnel: DoctorCheck[];
  service: DoctorCheck[];
  healthy: boolean;
}

// Plugins: integraciones de terceros que corren como proceso propio (su
// propia API HTTP, su propio puerto) — ver asterion-core/internal/plugins.
// Este frontend nunca sabe nada específico de un plugin puntual (SII, lo
// que sea): todo lo que se muestra sale del manifest (plugin.yaml) que
// declaró el propio plugin al instalarse.
export interface PluginConfigField {
  key: string;
  label: string;
  type: string;
  secret?: boolean;
  required?: boolean;
  default?: string;
}

export interface PluginManifest {
  name: string;
  version: string;
  description?: string;
  author?: string;
  repo?: string;
  start: { command: string; args?: string[] };
  port: number;
  health_path?: string;
  config_schema?: PluginConfigField[];
}

export type PluginStatus = "stopped" | "running" | "unhealthy";

export interface Plugin {
  external_ref: string;
  name: string;
  dir: string;
  manifest: PluginManifest;
  port?: number;
  pid?: number;
  status: PluginStatus;
  connected_project_id?: number;
  installed_at: string;
  updated_at: string;
}

export const api = {
  authStatus: () => request<AuthStatus>("/auth/status"),
  loginWithToken: (token: string) => request<{ ok: boolean }>("/auth/token", { method: "POST", body: JSON.stringify({ token }) }),
  logout: () => request<{ ok: boolean }>("/auth/logout", { method: "POST" }),
  me: () => request<{ authenticated: boolean }>("/me"),
  info: () => request<MachineInfo>("/info"),
  metrics: () => request<Snapshot>("/metrics"),
  costEstimate: (spec: { cpu_cores: number; ram_gb: number; storage_gb: number; storage_type: string }) =>
    request<CostEstimate>("/cost-estimate", { method: "POST", body: JSON.stringify(spec) }),
  runtimeStatus: () => request<RuntimeStatus>("/runtime/status"),
  runtimeDoctor: () => request<DoctorReport>("/runtime/doctor"),
  listPlugins: () => request<Plugin[]>("/plugins"),
  installPlugin: (repoUrl: string, name?: string) =>
    request<Plugin>("/plugins/install", { method: "POST", body: JSON.stringify({ repo_url: repoUrl, name }) }),
  removePlugin: (name: string) => request<{ removed: string }>(`/plugins/${name}`, { method: "DELETE" }),
  startPlugin: (name: string) => request<Plugin>(`/plugins/${name}/start`, { method: "POST" }),
  stopPlugin: (name: string) => request<Plugin>(`/plugins/${name}/stop`, { method: "POST" }),
  pluginConfig: (name: string) => request<Record<string, string>>(`/plugins/${name}/config`),
  updatePluginConfig: (name: string, values: Record<string, string>) =>
    request<{ updated: string; fields: number }>(`/plugins/${name}/config`, {
      method: "PUT",
      body: JSON.stringify({ values }),
    }),
  connectPlugin: (name: string, projectId: number) =>
    request<{ installed: Plugin; already_connected: boolean }>(`/plugins/${name}/connect`, {
      method: "POST",
      body: JSON.stringify({ project_id: projectId }),
    }),
};
