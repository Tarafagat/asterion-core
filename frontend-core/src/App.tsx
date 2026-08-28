import { useEffect, useState } from "react";
import type { FormEvent } from "react";
import {
  api,
  type AuthStatus,
  type CostEstimate,
  type DoctorReport,
  type MachineInfo,
  type Plugin,
  type PluginBrowseResult,
  type RuntimeStatus,
  type Snapshot,
} from "./lib/api";

export default function App() {
  const [authenticated, setAuthenticated] = useState<boolean | undefined>(undefined);
  const [authStatus, setAuthStatus] = useState<AuthStatus | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [loggingIn, setLoggingIn] = useState(false);
  const [shuttingDown, setShuttingDown] = useState(false);

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

  async function handleShutdown() {
    if (!window.confirm("¿Apagar el dashboard local? Vas a tener que correr 'asterion local serve' de nuevo para volver a entrar.")) {
      return;
    }
    await api.shutdown();
    setShuttingDown(true);
  }

  return (
    <div className="app-shell">
      <div className="topbar">
        <h1>Asterion Core</h1>
        <span className="badge">Dashboard local</span>
      </div>
      <main>
        {shuttingDown ? (
          <div className="card centered-message">
            <p className="section-title">Dashboard apagado</p>
            <p className="hint">
              El proceso de backend-core se detuvo. Corré <code>asterion local serve</code> (o{" "}
              <code>asterion local serve --background</code>) de nuevo cuando quieras volver a entrar.
            </p>
          </div>
        ) : authenticated === undefined ? null : authenticated ? (
          <Dashboard onLogout={handleLogout} onShutdown={handleShutdown} />
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

type DashboardTab = "overview" | "plugins";

function Dashboard({ onLogout, onShutdown }: { onLogout: () => void; onShutdown: () => void }) {
  const [tab, setTab] = useState<DashboardTab>("overview");
  const [info, setInfo] = useState<MachineInfo | null>(null);
  const [snapshot, setSnapshot] = useState<Snapshot | null>(null);
  const [cost, setCost] = useState<CostEstimate | null>(null);
  const [runtime, setRuntime] = useState<RuntimeStatus | null>(null);
  const [doctor, setDoctor] = useState<DoctorReport | null>(null);
  const [runtimeError, setRuntimeError] = useState<string | null>(null);
  const [plugins, setPlugins] = useState<Plugin[] | null>(null);
  const [pluginsError, setPluginsError] = useState<string | null>(null);
  const [selectedPlugin, setSelectedPlugin] = useState<string | null>(null);

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

  async function refreshPlugins() {
    try {
      setPlugins(await api.listPlugins());
      setPluginsError(null);
    } catch (err) {
      setPluginsError(err instanceof Error ? err.message : "No se pudieron listar los plugins.");
    }
  }

  useEffect(() => {
    refreshPlugins();
  }, []);

  const runningPlugins = plugins?.filter((p) => p.status === "running").length ?? 0;

  return (
    <div className="dashboard">
      <nav className="subnav">
        <div className="subnav-tabs">
          <button className={`subnav-tab ${tab === "overview" ? "active" : ""}`} onClick={() => setTab("overview")}>
            Resumen
          </button>
          <button
            className={`subnav-tab ${tab === "plugins" ? "active" : ""}`}
            onClick={() => {
              setTab("plugins");
              setSelectedPlugin(null);
            }}
          >
            Plugins
            {plugins && plugins.length > 0 && (
              <span className="tab-count">{runningPlugins}/{plugins.length}</span>
            )}
          </button>
        </div>
        <div className="subnav-actions">
          <button className="pill-btn outline" onClick={onLogout}>
            Cerrar sesión
          </button>
          <button className="pill-btn" onClick={onShutdown} title="Detiene el proceso de backend-core en esta máquina">
            Apagar
          </button>
        </div>
      </nav>

      {tab === "overview" && (
        <>
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
        </>
      )}

      {tab === "plugins" && selectedPlugin === null && (
        <PluginsPanel
          plugins={plugins}
          error={pluginsError}
          onChange={refreshPlugins}
          onOpen={(name) => setSelectedPlugin(name)}
        />
      )}

      {tab === "plugins" && selectedPlugin !== null && (
        <PluginDetail
          plugin={plugins?.find((p) => p.name === selectedPlugin) ?? null}
          onBack={() => setSelectedPlugin(null)}
          onChange={refreshPlugins}
        />
      )}
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

type InstallMode = "repo" | "folder";

function PluginsPanel({
  plugins,
  error,
  onChange,
  onOpen,
}: {
  plugins: Plugin[] | null;
  error: string | null;
  onChange: () => void;
  onOpen: (name: string) => void;
}) {
  const [mode, setMode] = useState<InstallMode>("repo");
  const [repoUrl, setRepoUrl] = useState("");
  const [installing, setInstalling] = useState(false);
  const [installError, setInstallError] = useState<string | null>(null);

  async function handleInstallRepo(e: FormEvent) {
    e.preventDefault();
    if (!repoUrl.trim()) return;
    setInstalling(true);
    setInstallError(null);
    try {
      await api.installPlugin(repoUrl.trim());
      setRepoUrl("");
      onChange();
    } catch (err) {
      setInstallError(err instanceof Error ? err.message : "No se pudo instalar el plugin.");
    } finally {
      setInstalling(false);
    }
  }

  async function handleInstallFolder(folderPath: string, name: string) {
    setInstalling(true);
    setInstallError(null);
    try {
      await api.installPlugin(folderPath, name.trim() || undefined, true);
      onChange();
      return true;
    } catch (err) {
      setInstallError(err instanceof Error ? err.message : "No se pudo vincular esa carpeta.");
      return false;
    } finally {
      setInstalling(false);
    }
  }

  return (
    <div className="card">
      <p className="section-title">Plugins</p>
      <p className="hint">
        Integraciones de terceros que corren como proceso propio, en su propio puerto local. Instalalas desde el
        repo de GitHub del plugin (público o privado, según tus credenciales de git) o vinculando directamente una
        carpeta de este disco — para desarrollar o usar un plugin privado sin necesitar ni siquiera un repo git.
        Nunca se ejecuta nada del plugin salvo lo que su plugin.yaml declara. Una vez instalado y arrancado, sus
        recursos y acciones (declarados en su plugin.yaml) aparecen abajo, listos para usar desde acá.
      </p>

      <div className="install-tabs">
        <button
          type="button"
          className={mode === "repo" ? "install-tab active" : "install-tab"}
          onClick={() => setMode("repo")}
        >
          Desde repo (git)
        </button>
        <button
          type="button"
          className={mode === "folder" ? "install-tab active" : "install-tab"}
          onClick={() => setMode("folder")}
        >
          Desde carpeta local
        </button>
      </div>

      {mode === "repo" && (
        <form onSubmit={handleInstallRepo} style={{ marginTop: "0.85rem", display: "flex", gap: "0.5rem" }}>
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
      )}

      {mode === "folder" && <FolderInstaller installing={installing} onInstall={handleInstallFolder} />}

      {(installError || error) && <p className="error-text">{installError ?? error}</p>}
      {plugins && plugins.length === 0 && (
        <p className="hint" style={{ marginTop: "0.85rem" }}>
          Todavía no hay plugins instalados en esta máquina.
        </p>
      )}
      {plugins?.map((p) => (
        <PluginCard key={p.name} plugin={p} onChange={onChange} onOpen={() => onOpen(p.name)} />
      ))}
    </div>
  );
}

// Panel a pantalla completa de un plugin puntual: embebe su propio
// frontend (servido por su propio proceso Go, en su propio puerto) dentro
// del dashboard local, vía el reverse proxy que backend-core ya expone
// para cualquier método/path (proxy_to_plugin). El truco que lo hace
// funcionar: la URL termina en "/" (que backend-core interpreta como
// path="" y reenvía a la raíz del plugin, es decir su index.html), y cada
// frontend de plugin arma sus propios fetch() con rutas relativas (sin
// barra inicial) — así "api/v1/x" resuelve contra este mismo prefijo en
// vez de contra la raíz del dashboard.
function PluginDetail({
  plugin,
  onBack,
  onChange,
}: {
  plugin: Plugin | null;
  onBack: () => void;
  onChange: () => void;
}) {
  const [busy, setBusy] = useState(false);

  async function handleStart() {
    setBusy(true);
    try {
      await api.startPlugin(plugin!.name);
      onChange();
    } finally {
      setBusy(false);
    }
  }

  if (!plugin) {
    return (
      <div className="card">
        <button className="small-btn" onClick={onBack}>
          ← Volver a Plugins
        </button>
        <p className="hint" style={{ marginTop: "0.75rem" }}>
          Este plugin ya no está instalado.
        </p>
      </div>
    );
  }

  return (
    <div className="plugin-detail">
      <div className="plugin-detail-header">
        <button className="small-btn" onClick={onBack}>
          ← Volver a Plugins
        </button>
        <div className="plugin-detail-title">
          <span className="plugin-name">{plugin.manifest.name}</span>
          <span className="plugin-version">v{plugin.manifest.version}</span>
          <span className={`status-dot ${plugin.status === "running" ? "status-running" : plugin.status === "unhealthy" ? "status-unhealthy" : "status-stopped"}`} />
          <span className="hint">{plugin.status}</span>
        </div>
      </div>

      {plugin.status === "running" ? (
        <iframe
          className="plugin-detail-frame"
          src={`/api/plugins/${plugin.name}/proxy/`}
          title={`Panel de ${plugin.manifest.name}`}
        />
      ) : (
        <div className="card" style={{ marginTop: "0.85rem" }}>
          <p className="hint">Este plugin no está corriendo — arrancalo para ver su panel.</p>
          <button className="small-btn" style={{ marginTop: "0.6rem" }} disabled={busy} onClick={handleStart}>
            {busy ? "Arrancando…" : "Arrancar"}
          </button>
        </div>
      )}

      <PluginCharacteristicsCard plugin={plugin} />
      <PluginConfigCard plugin={plugin} onChange={onChange} />
      <PluginEndpointsCard plugin={plugin} />
      <PluginConnectCard plugin={plugin} onChange={onChange} />
    </div>
  );
}

// Tabla con los datos "de identidad" del plugin — todo lo que ya viene
// declarado en su manifest (Asterion Plugin Contract) más la dirección
// real donde está corriendo su API ahora mismo, dato que no vive en el
// manifest (depende del puerto libre que le tocó en este arranque).
function PluginCharacteristicsCard({ plugin }: { plugin: Plugin }) {
  const m = plugin.manifest;
  const perms = m.permissions;
  const permsSummary = perms
    ? [
        perms.network && perms.network.length > 0 ? `red: ${perms.network.join(", ")}` : null,
        perms.filesystem && perms.filesystem.length > 0 ? `filesystem: ${perms.filesystem.join(", ")}` : null,
        perms.database ? "base de datos" : null,
        perms.secrets ? "secretos" : null,
      ]
        .filter(Boolean)
        .join(" · ") || "ninguno declarado"
    : "ninguno declarado";

  const rows: [string, string][] = [
    ["Descripción", m.description || "—"],
    ["Autor", m.author || "—"],
    ["Licencia", m.license || "—"],
    ["Contract version", m.contract_version || "—"],
    ["Lenguaje", m.language ? `${m.language.name ?? "?"}${m.language.version ? ` ${m.language.version}` : ""}` : "—"],
    ["Dirección de la API", plugin.status === "running" && plugin.port ? `127.0.0.1:${plugin.port}` : "no está corriendo"],
    ["Health check", m.health_path || "—"],
    ["Permisos declarados", permsSummary],
    ["Referencia externa", plugin.external_ref],
  ];

  return (
    <div className="card" style={{ marginTop: "0.85rem" }}>
      <p className="section-title">Características</p>
      <table className="detail-table">
        <tbody>
          {rows.map(([label, value]) => (
            <tr key={label}>
              <th>{label}</th>
              <td>{value}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

function PluginConfigCard({ plugin, onChange }: { plugin: Plugin; onChange: () => void }) {
  const [values, setValues] = useState<Record<string, string>>({});
  const [loaded, setLoaded] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [saved, setSaved] = useState(false);
  const fields = plugin.manifest.config_schema ?? [];

  useEffect(() => {
    if (fields.length === 0) return;
    api
      .pluginConfig(plugin.name)
      .then((current) => {
        // Los campos 'secret' vuelven enmascarados ("••••••••") — precargarlos
        // reenviaría la máscara como si fuera el valor real y pisaría el
        // secreto guardado. Solo se precargan los campos no secretos.
        const prefill: Record<string, string> = {};
        for (const field of fields) {
          if (!field.secret && current[field.key] !== undefined) {
            prefill[field.key] = current[field.key];
          }
        }
        setValues(prefill);
        setLoaded(true);
      })
      .catch((err) => setError(err instanceof Error ? err.message : "No se pudo leer la configuración."));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [plugin.name]);

  if (fields.length === 0) return null;

  async function handleSubmit(e: FormEvent) {
    e.preventDefault();
    const changed = Object.fromEntries(Object.entries(values).filter(([, v]) => v !== ""));
    if (Object.keys(changed).length === 0) return;
    setBusy(true);
    setError(null);
    setSaved(false);
    try {
      await api.updatePluginConfig(plugin.name, changed);
      setSaved(true);
      onChange();
    } catch (err) {
      setError(err instanceof Error ? err.message : "No se pudo guardar la configuración.");
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="card" style={{ marginTop: "0.85rem" }}>
      <p className="section-title">Configuración</p>
      {!loaded && !error && <p className="hint">Cargando…</p>}
      {loaded && (
        <form onSubmit={handleSubmit}>
          {fields.map((field) => (
            <div className="field-row" key={field.key}>
              <label htmlFor={`${plugin.name}-${field.key}`}>
                {field.label}
                {field.required ? " *" : ""}
              </label>
              <input
                id={`${plugin.name}-${field.key}`}
                className="token-input"
                type={field.secret ? "password" : "text"}
                value={values[field.key] ?? ""}
                placeholder={field.secret ? "sin cambios" : field.default}
                onChange={(e) => {
                  setSaved(false);
                  setValues({ ...values, [field.key]: e.target.value });
                }}
              />
            </div>
          ))}
          <button className="small-btn" type="submit" disabled={busy} style={{ marginTop: "0.6rem" }}>
            {busy ? "Guardando…" : "Guardar config"}
          </button>
          {saved && <span className="success-text" style={{ marginLeft: "0.6rem" }}>Guardado.</span>}
        </form>
      )}
      {error && <p className="error-text">{error}</p>}
    </div>
  );
}

// Solo lista — a propósito no ejecuta nada acá (a diferencia de una
// versión anterior de este panel que sí "probaba" cada endpoint en vivo).
// El lugar para usar el plugin de verdad es su propio panel embebido más
// arriba; esto es catálogo de referencia: qué hay, en qué método/ruta, y
// qué estructura espera.
function PluginEndpointsCard({ plugin }: { plugin: Plugin }) {
  const resources = plugin.manifest.resources ?? [];
  const actions = plugin.manifest.actions ?? [];
  if (resources.length === 0 && actions.length === 0) return null;

  const basePath = plugin.manifest.api?.base_path ?? "";
  const crudMethod: Record<string, string> = { list: "GET", read: "GET /{id}", create: "POST", update: "PUT/PATCH /{id}", delete: "DELETE /{id}" };

  return (
    <div className="card" style={{ marginTop: "0.85rem" }}>
      <p className="section-title">Endpoints</p>
      <table className="detail-table endpoints-table">
        <thead>
          <tr>
            <th>Método</th>
            <th>Ruta</th>
            <th>Qué es / estructura</th>
          </tr>
        </thead>
        <tbody>
          {resources.map((r) =>
            (r.crud ?? []).map((op) => (
              <tr key={`${r.name}-${op}`}>
                <td className="endpoint-method">{crudMethod[op] ?? op}</td>
                <td className="endpoint-path">{basePath + r.endpoint}</td>
                <td className="hint">
                  recurso <strong>{r.name}</strong>
                  {r.primary_key ? ` · clave: ${r.primary_key}` : ""}
                  {r.schema ? ` · schema: ${r.schema}` : ""}
                </td>
              </tr>
            )),
          )}
          {actions.map((a) => (
            <tr key={a.name}>
              <td className="endpoint-method">{a.method}</td>
              <td className="endpoint-path">{basePath + a.endpoint}</td>
              <td className="hint">
                acción <strong>{a.name}</strong>
                {a.description ? ` — ${a.description}` : ""}
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

function PluginConnectCard({ plugin, onChange }: { plugin: Plugin; onChange: () => void }) {
  const [projectId, setProjectId] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  if (plugin.connected_project_id) {
    return (
      <div className="card" style={{ marginTop: "0.85rem" }}>
        <p className="section-title">Asterion Cloud</p>
        <p className="hint">Conectado al proyecto {plugin.connected_project_id}.</p>
      </div>
    );
  }

  async function handleConnect(e: FormEvent) {
    e.preventDefault();
    const id = parseInt(projectId, 10);
    if (!id) return;
    setBusy(true);
    setError(null);
    try {
      await api.connectPlugin(plugin.name, id);
      onChange();
    } catch (err) {
      setError(err instanceof Error ? err.message : "No se pudo conectar.");
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="card" style={{ marginTop: "0.85rem" }}>
      <p className="section-title">Asterion Cloud</p>
      <form onSubmit={handleConnect} style={{ display: "flex", gap: "0.5rem", alignItems: "center" }}>
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
          {busy ? "Conectando…" : "Conectar a Cloud"}
        </button>
      </form>
      {error && <p className="error-text" style={{ marginTop: "0.5rem" }}>{error}</p>}
    </div>
  );
}

// Selector de carpeta para 'asterion plugin install --link': un navegador
// de directorios servido por backend-core (GET /api/plugins/browse-dirs),
// no un <input type="file"> — el navegador nunca expone la ruta absoluta
// real en disco fuera de Electron, así que "elegir carpeta" se resuelve
// navegando por HTTP contra el propio backend local, que sí tiene acceso
// directo al filesystem de esta máquina.
function FolderInstaller({
  installing,
  onInstall,
}: {
  installing: boolean;
  onInstall: (folderPath: string, name: string) => Promise<boolean>;
}) {
  const [browse, setBrowse] = useState<PluginBrowseResult | null>(null);
  const [browseError, setBrowseError] = useState<string | null>(null);
  const [browseLoading, setBrowseLoading] = useState(false);
  const [pathInput, setPathInput] = useState("");
  const [name, setName] = useState("");

  async function loadDir(path?: string) {
    setBrowseLoading(true);
    setBrowseError(null);
    try {
      const result = await api.browsePluginDirs(path);
      setBrowse(result);
      setPathInput(result.path);
    } catch (err) {
      setBrowseError(err instanceof Error ? err.message : "No se pudo leer esa carpeta.");
    } finally {
      setBrowseLoading(false);
    }
  }

  useEffect(() => {
    if (!browse && !browseLoading) void loadDir();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  async function handleGoToPath(e: FormEvent) {
    e.preventDefault();
    if (pathInput.trim()) void loadDir(pathInput.trim());
  }

  async function handleLink() {
    if (!browse) return;
    const ok = await onInstall(browse.path, name);
    if (ok) setName("");
  }

  return (
    <div className="folder-browser">
      <form onSubmit={handleGoToPath} style={{ display: "flex", gap: "0.5rem" }}>
        <input
          type="text"
          value={pathInput}
          onChange={(e) => setPathInput(e.target.value)}
          placeholder="/ruta/a/tu/plugin"
          className="token-input"
        />
        <button className="small-btn" type="submit" disabled={browseLoading}>
          Ir
        </button>
      </form>

      {browseError && <p className="error-text">{browseError}</p>}

      {browse && (
        <>
          <div className="folder-browser-path">
            <code>{browse.path}</code>
            {browse.has_manifest && <span className="badge">plugin.yaml ✓</span>}
          </div>
          <div className="folder-list">
            {browse.parent && (
              <button type="button" className="folder-entry" onClick={() => void loadDir(browse.parent!)}>
                ⬆ ..
              </button>
            )}
            {browse.entries.map((entry) => (
              <button
                key={entry.path}
                type="button"
                className="folder-entry"
                onClick={() => void loadDir(entry.path)}
              >
                {entry.has_manifest ? "📦 " : "📁 "}
                {entry.name}
              </button>
            ))}
            {browse.entries.length === 0 && <p className="hint">Sin subcarpetas acá.</p>}
          </div>

          <div className="field-row">
            <label>Nombre (opcional)</label>
            <input
              type="text"
              className="token-input"
              value={name}
              onChange={(e) => setName(e.target.value)}
              placeholder="usa el 'name' del plugin.yaml si se deja vacío"
            />
          </div>
          <button
            type="button"
            className="small-btn"
            style={{ marginTop: "0.6rem" }}
            disabled={installing}
            onClick={() => void handleLink()}
          >
            {installing
              ? "Vinculando…"
              : browse.has_manifest
                ? "Vincular esta carpeta"
                : "Vincular esta carpeta (todavía sin plugin.yaml)"}
          </button>
        </>
      )}
    </div>
  );
}

// PluginCard es deliberadamente chica — solo lo justo para identificar el
// plugin y decidir qué hacer con él. Todo el detalle (características,
// configuración, endpoints, conectar a Cloud) vive en PluginDetail, la
// vista que abre "Abrir panel" — así la lista no se vuelve una pared de
// tarjetas gigantes cuando hay varios plugins instalados.
function PluginCard({ plugin, onChange, onOpen }: { plugin: Plugin; onChange: () => void; onOpen: () => void }) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

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

  const statusClass =
    plugin.status === "running" ? "status-running" : plugin.status === "unhealthy" ? "status-unhealthy" : "status-stopped";

  return (
    <div className="plugin-item plugin-item-compact">
      <div className="plugin-header">
        <div>
          <span className={`status-dot ${statusClass}`} />
          <span className="plugin-name">{plugin.manifest.name}</span>
          <span className="plugin-version">v{plugin.manifest.version}</span>
        </div>
      </div>

      {plugin.manifest.description && (
        <p className="hint plugin-desc-clamp" style={{ marginTop: "0.35rem" }}>
          {plugin.manifest.description}
        </p>
      )}

      <div className="btn-row" style={{ marginTop: "0.65rem", alignItems: "center" }}>
        {plugin.status === "running" && plugin.port && <span className="api-address">127.0.0.1:{plugin.port}</span>}
        <button className="small-btn" onClick={onOpen}>
          Abrir panel
        </button>
        {plugin.status === "running" ? (
          <button className="small-btn" disabled={busy} onClick={() => run(() => api.stopPlugin(plugin.name))}>
            Detener
          </button>
        ) : (
          <button className="small-btn" disabled={busy} onClick={() => run(() => api.startPlugin(plugin.name))}>
            Arrancar
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

      {error && <p className="error-text">{error}</p>}
    </div>
  );
}

