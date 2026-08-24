import { useEffect, useState } from "react";
import type { FormEvent } from "react";
import {
  api,
  type AuthStatus,
  type CostEstimate,
  type DoctorReport,
  type MachineInfo,
  type Plugin,
  type RuntimeStatus,
  type Snapshot,
} from "./lib/api";

export default function App() {
  const [authenticated, setAuthenticated] = useState<boolean | undefined>(undefined);
  const [authStatus, setAuthStatus] = useState<AuthStatus | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [loggingIn, setLoggingIn] = useState(false);

  useEffect(() => {
    api.authStatus().then(setAuthStatus);
    api
      .me()
      .then(() => setAuthenticated(true))
      .catch(() => setAuthenticated(false));
  }, []);

  async function handleTokenLogin(token: string) {
    setError(null);
    setLoggingIn(true);
    try {
      await api.loginWithToken(token);
      setAuthenticated(true);
    } catch (err) {
      setError(err instanceof Error ? err.message : "No se pudo iniciar sesión.");
    } finally {
      setLoggingIn(false);
    }
  }

  async function handleLogout() {
    await api.logout();
    setAuthenticated(false);
  }

  return (
    <div className="app-shell">
      <div className="topbar">
        <h1>Asterion Core</h1>
        <span className="badge">Dashboard local</span>
      </div>
      <main>
        {authenticated === undefined ? null : authenticated ? (
          <Dashboard onLogout={handleLogout} />
        ) : (
          <LoginScreen authStatus={authStatus} error={error} loggingIn={loggingIn} onLogin={handleTokenLogin} />
        )}
      </main>
    </div>
  );
}

function LoginScreen({
  authStatus,
  error,
  loggingIn,
  onLogin,
}: {
  authStatus: AuthStatus | null;
  error: string | null;
  loggingIn: boolean;
  onLogin: (token: string) => void;
}) {
  const [token, setToken] = useState("");

  return (
    <div className="card centered-message">
      <p className="hint">
        Este dashboard corre en tu propia máquina y solo muestra datos crudos (CPU, RAM, disco, red) y un costo
        estimado — no factura nada, eso es exclusivo de Asterion Cloud.
      </p>
      {authStatus?.configured ? (
        <p className="hint" style={{ marginTop: "0.75rem" }}>
          Pegá el token que te mostró <code>asterion local serve</code> en la terminal la primera vez que lo
          corriste. Si lo perdiste, corré <code>asterion local auth rotate</code> para generar uno nuevo.
        </p>
      ) : (
        <p className="hint" style={{ marginTop: "0.75rem" }}>
          Todavía no hay un token generado en esta máquina — corré <code>asterion local serve</code> (o{" "}
          <code>asterion local auth rotate</code>) desde la terminal primero.
        </p>
      )}
      <form
        onSubmit={(e) => {
          e.preventDefault();
          if (token.trim()) onLogin(token.trim());
        }}
        style={{ marginTop: "1rem", display: "flex", gap: "0.5rem" }}
      >
        <input
          type="password"
          value={token}
          onChange={(e) => setToken(e.target.value)}
          placeholder="Token de acceso"
          className="token-input"
          autoFocus
        />
        <button className="primary-btn" type="submit" disabled={!token.trim() || loggingIn} style={{ marginTop: 0 }}>
          {loggingIn ? "Entrando…" : "Entrar"}
        </button>
      </form>
      {error && <p className="error-text">{error}</p>}
    </div>
  );
}

function Dashboard({ onLogout }: { onLogout: () => void }) {
  const [info, setInfo] = useState<MachineInfo | null>(null);
  const [snapshot, setSnapshot] = useState<Snapshot | null>(null);
  const [cost, setCost] = useState<CostEstimate | null>(null);
  const [runtime, setRuntime] = useState<RuntimeStatus | null>(null);
  const [doctor, setDoctor] = useState<DoctorReport | null>(null);
  const [runtimeError, setRuntimeError] = useState<string | null>(null);

  useEffect(() => {
    api.info().then(setInfo);
    Promise.all([api.runtimeStatus(), api.runtimeDoctor()])
      .then(([s, d]) => {
        setRuntime(s);
        setDoctor(d);
      })
      .catch((err) => setRuntimeError(err instanceof Error ? err.message : "No se pudo consultar el Runtime Engine."));
  }, []);

  useEffect(() => {
    let cancelled = false;
    async function tick() {
      const snap = await api.metrics();
      if (cancelled) return;
      setSnapshot(snap);
      if (info) {
        const estimate = await api.costEstimate({
          cpu_cores: info.cpu_cores,
          ram_gb: snap.ram_total_gb,
          storage_gb: snap.disk_total_gb,
          storage_type: "ssd",
        });
        if (!cancelled) setCost(estimate);
      }
    }
    tick();
    const id = setInterval(tick, 5000);
    return () => {
      cancelled = true;
      clearInterval(id);
    };
  }, [info]);

  return (
    <div className="dashboard">
      <div style={{ display: "flex", justifyContent: "space-between", alignItems: "center" }}>
        <span className="hint">Sesión local activa</span>
        <button className="link" onClick={onLogout}>
          Cerrar sesión
        </button>
      </div>

      {info && (
        <div className="card">
          <p className="section-title">Esta máquina</p>
          <div className="grid">
            <Stat label="Host" value={info.hostname} />
            <Stat label="SO" value={`${info.os} / ${info.architecture}`} />
            <Stat label="Tipo" value={info.virtualization} />
            <Stat label="CPU" value={`${info.cpu_cores} núcleos`} />
          </div>
        </div>
      )}

      {snapshot && (
        <div className="card">
          <p className="section-title">Uso ahora (datos crudos)</p>
          <div className="grid">
            <Stat label="CPU" value={`${snapshot.cpu_percent.toFixed(1)}%`} />
            <Stat label="RAM" value={`${snapshot.ram_used_gb} / ${snapshot.ram_total_gb} GB`} />
            <Stat label="Disco" value={`${snapshot.disk_used_gb} / ${snapshot.disk_total_gb} GB`} />
            <Stat label="Red (acum.)" value={`↓${snapshot.network_in_gb} ↑${snapshot.network_out_gb} GB`} />
          </div>
        </div>
      )}

      {cost && (
        <div className="card">
          <p className="section-title">Costo estimado (open source, tabla pública)</p>
          <div className="total-cost">${cost.estimated_monthly_total} / mes</div>
          <p className="hint" style={{ marginTop: "0.5rem" }}>
            Fuente de precios: {cost.price_source === "asterion-cloud-live" ? "en vivo desde Asterion Cloud" : "tabla pública por defecto"}.
            Esto es una estimación de referencia — la tarifa real de facturación (si conectás esta instancia a un
            proyecto) la define Asterion Cloud.
          </p>
        </div>
      )}

      {runtimeError && (
        <div className="card">
          <p className="section-title">Runtime</p>
          <p className="error-text">{runtimeError}</p>
        </div>
      )}

      {runtime && (
        <div className="card">
          <p className="section-title">Runtime detectado</p>
          <div className="grid">
            <Stat label="Service manager" value={runtime.environment.service_manager} />
            <Stat label="Privilegios" value={runtime.environment.privileges} />
            <Stat label="Firewall" value={runtime.environment.firewall.join(", ") || "ninguno"} />
            <Stat label="Reverse proxy" value={runtime.environment.reverse_proxy.join(", ") || "ninguno"} />
            <Stat label="Tunnel" value={runtime.environment.tunnel.join(", ") || "ninguno"} />
            <Stat label="Puerto configurado" value={`${runtime.config.service_bind}:${runtime.config.service_port}`} />
          </div>
        </div>
      )}

      {doctor && (
        <div className="card">
          <p className="section-title">
            asterion local doctor — {doctor.healthy ? "✓ saludable" : "✗ encontró problemas"}
          </p>
          <DoctorList title="Runtime" checks={doctor.runtime} />
          <DoctorList title="Seguridad" checks={doctor.security} />
          <DoctorList title="Reverse proxy" checks={doctor.reverse_proxy} />
          <DoctorList title="Tunnel" checks={doctor.tunnel} />
          <DoctorList title="Servicio" checks={doctor.service} />
        </div>
      )}

      <PluginsPanel />
    </div>
  );
}

function DoctorList({ title, checks }: { title: string; checks: DoctorReport["runtime"] }) {
  return (
    <div style={{ marginTop: "0.75rem" }}>
      <p className="label" style={{ marginBottom: "0.35rem" }}>
        {title}
      </p>
      {checks.map((check) => (
        <div key={check.name} className="hint" style={{ display: "flex", gap: "0.5rem", marginBottom: "0.2rem" }}>
          <span>{check.pass === null ? "•" : check.pass ? "✓" : "✗"}</span>
          <span>
            {check.name}: {check.detail}
          </span>
        </div>
      ))}
    </div>
  );
}

function Stat({ label, value }: { label: string; value: string }) {
  return (
    <div className="stat">
      <div className="label">{label}</div>
      <div className="value">{value}</div>
    </div>
  );
}

function PluginsPanel() {
  const [pluginsList, setPluginsList] = useState<Plugin[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [repoUrl, setRepoUrl] = useState("");
  const [installing, setInstalling] = useState(false);

  async function refresh() {
    try {
      const list = await api.listPlugins();
      setPluginsList(list);
    } catch (err) {
      setError(err instanceof Error ? err.message : "No se pudieron listar los plugins.");
    }
  }

  useEffect(() => {
    refresh();
  }, []);

  async function handleInstall(e: FormEvent) {
    e.preventDefault();
    if (!repoUrl.trim()) return;
    setInstalling(true);
    setError(null);
    try {
      await api.installPlugin(repoUrl.trim());
      setRepoUrl("");
      await refresh();
    } catch (err) {
      setError(err instanceof Error ? err.message : "No se pudo instalar el plugin.");
    } finally {
      setInstalling(false);
    }
  }

  return (
    <div className="card">
      <p className="section-title">Plugins</p>
      <p className="hint">
        Integraciones de terceros que corren como proceso propio, en su propio puerto local — instalalas desde el
        repo de GitHub del plugin (público o privado, según tus credenciales de git). Nunca se ejecuta nada del repo
        salvo lo que su plugin.yaml declara.
      </p>
      <form onSubmit={handleInstall} style={{ marginTop: "0.85rem", display: "flex", gap: "0.5rem" }}>
        <input
          type="text"
          value={repoUrl}
          onChange={(e) => setRepoUrl(e.target.value)}
          placeholder="https://github.com/usuario/asterion-plugin-sii"
          className="token-input"
        />
        <button className="small-btn" type="submit" disabled={!repoUrl.trim() || installing}>
          {installing ? "Instalando…" : "Instalar"}
        </button>
      </form>
      {error && <p className="error-text">{error}</p>}
      {pluginsList && pluginsList.length === 0 && (
        <p className="hint" style={{ marginTop: "0.85rem" }}>
          Todavía no hay plugins instalados en esta máquina.
        </p>
      )}
      {pluginsList?.map((p) => (
        <PluginCard key={p.name} plugin={p} onChange={refresh} />
      ))}
    </div>
  );
}

function PluginCard({ plugin, onChange }: { plugin: Plugin; onChange: () => void }) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [configValues, setConfigValues] = useState<Record<string, string>>({});
  const [showConfig, setShowConfig] = useState(false);
  const [projectId, setProjectId] = useState("");

  async function run(action: () => Promise<unknown>) {
    setBusy(true);
    setError(null);
    try {
      await action();
      onChange();
    } catch (err) {
      setError(err instanceof Error ? err.message : "Ocurrió un error.");
    } finally {
      setBusy(false);
    }
  }

  async function loadConfig() {
    try {
      const current = await api.pluginConfig(plugin.name);
      // Los campos 'secret' vuelven enmascarados ("••••••••") — precargarlos
      // reenviaría la máscara como si fuera el valor real y pisaría el
      // secreto guardado. Solo se precargan los campos no secretos.
      const prefill: Record<string, string> = {};
      for (const field of plugin.manifest.config_schema ?? []) {
        if (!field.secret && current[field.key] !== undefined) {
          prefill[field.key] = current[field.key];
        }
      }
      setConfigValues(prefill);
      setShowConfig(true);
    } catch (err) {
      setError(err instanceof Error ? err.message : "No se pudo leer la configuración.");
    }
  }

  async function handleSaveConfig(e: FormEvent) {
    e.preventDefault();
    const values = Object.fromEntries(Object.entries(configValues).filter(([, v]) => v !== ""));
    if (Object.keys(values).length === 0) return;
    await run(() => api.updatePluginConfig(plugin.name, values));
  }

  async function handleConnect(e: FormEvent) {
    e.preventDefault();
    const id = parseInt(projectId, 10);
    if (!id) return;
    await run(() => api.connectPlugin(plugin.name, id));
  }

  const statusClass =
    plugin.status === "running" ? "status-running" : plugin.status === "unhealthy" ? "status-unhealthy" : "status-stopped";

  return (
    <div className="plugin-item">
      <div className="plugin-header">
        <div>
          <span className={`status-dot ${statusClass}`} />
          <span className="plugin-name">{plugin.manifest.name}</span>
          <span className="plugin-version">v{plugin.manifest.version}</span>
        </div>
        <span className="plugin-ref">{plugin.external_ref}</span>
      </div>

      {plugin.manifest.description && (
        <p className="hint" style={{ marginTop: "0.4rem" }}>
          {plugin.manifest.description}
        </p>
      )}
      <p className="hint" style={{ marginTop: "0.3rem" }}>
        Estado: {plugin.status}
        {plugin.port ? ` · puerto ${plugin.port}` : ""}
        {plugin.connected_project_id ? ` · conectado a proyecto ${plugin.connected_project_id} de Asterion Cloud` : ""}
      </p>

      <div className="btn-row" style={{ marginTop: "0.75rem" }}>
        {plugin.status === "running" ? (
          <button className="small-btn" disabled={busy} onClick={() => run(() => api.stopPlugin(plugin.name))}>
            Detener
          </button>
        ) : (
          <button className="small-btn" disabled={busy} onClick={() => run(() => api.startPlugin(plugin.name))}>
            Arrancar
          </button>
        )}
        {plugin.manifest.config_schema && plugin.manifest.config_schema.length > 0 && (
          <button className="small-btn" disabled={busy} onClick={loadConfig}>
            Configurar
          </button>
        )}
        <button
          className="danger-btn"
          disabled={busy}
          onClick={() => {
            if (window.confirm(`¿Desinstalar ${plugin.name}? Esto borra el repo clonado y su configuración guardada.`)) {
              run(() => api.removePlugin(plugin.name));
            }
          }}
        >
          Desinstalar
        </button>
      </div>

      {showConfig && (
        <form onSubmit={handleSaveConfig} style={{ marginTop: "0.75rem" }}>
          {plugin.manifest.config_schema?.map((field) => (
            <div className="field-row" key={field.key}>
              <label htmlFor={`${plugin.name}-${field.key}`}>
                {field.label}
                {field.required ? " *" : ""}
              </label>
              <input
                id={`${plugin.name}-${field.key}`}
                className="token-input"
                type={field.secret ? "password" : "text"}
                value={configValues[field.key] ?? ""}
                placeholder={field.secret ? "sin cambios" : field.default}
                onChange={(e) => setConfigValues({ ...configValues, [field.key]: e.target.value })}
              />
            </div>
          ))}
          <button className="small-btn" type="submit" disabled={busy} style={{ marginTop: "0.6rem" }}>
            Guardar config
          </button>
        </form>
      )}

      {!plugin.connected_project_id && (
        <form
          onSubmit={handleConnect}
          style={{ marginTop: "0.75rem", display: "flex", gap: "0.5rem", alignItems: "center" }}
        >
          <input
            type="number"
            min={1}
            className="token-input"
            style={{ maxWidth: "8rem", padding: "0.45rem 0.7rem" }}
            placeholder="ID proyecto Cloud"
            value={projectId}
            onChange={(e) => setProjectId(e.target.value)}
          />
          <button className="small-btn" type="submit" disabled={busy || !projectId}>
            Conectar a Cloud
          </button>
        </form>
      )}

      {error && <p className="error-text">{error}</p>}
    </div>
  );
}
