package runtime

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// RemoteManagementPermissions son los permisos granulares que el
// administrador local le da a Asterion Cloud sobre esta máquina (spec
// §29) — todos false por defecto: instalar el agente nunca implica ceder
// control automáticamente, cada permiso se habilita a mano.
type RemoteManagementPermissions struct {
	ReadMetrics         bool `json:"read_metrics"`
	ReadLogs            bool `json:"read_logs"`
	RestartServices     bool `json:"restart_services"`
	RunDoctor           bool `json:"run_doctor"`
	RunRepair           bool `json:"run_repair"`
	ModifyConfiguration bool `json:"modify_configuration"`
	ModifyFirewall      bool `json:"modify_firewall"`
	ModifyNetwork       bool `json:"modify_network"`
	ModifyUsers         bool `json:"modify_users"`
}

// Config es la configuración persistente del Runtime local. Spec §6
// propone /etc/asterion/asterion.yaml en YAML; acá se guarda en
// ~/.config/asterion/runtime.json — deliberadamente distinto:
//  1. El resto del CLI ya usa ~/.config/asterion/ para todo (config.json,
//     credentials.json, instances.json) sin pedir root, y `install-agent`
//     ya usa systemd --user en vez de un servicio de sistema. Pedir un
//     archivo en /etc además rompería ese modelo sin servir para nada hoy.
//  2. JSON en vez de YAML evita sumar una dependencia nueva solo para esto
//     (el proyecto no tenía ningún parser YAML) — mismo contenido, mismo
//     esquema conceptual, distinto formato de archivo.
//
// Si más adelante Asterion se instala como servicio de sistema (root,
// multiusuario real), migrar a /etc/asterion/ + YAML es un cambio acotado
// a este archivo, no al resto del CLI.
type Config struct {
	RuntimeName       string                      `json:"runtime_name"`
	ServiceBind       string                      `json:"service_bind"`
	ServicePort       int                         `json:"service_port"`
	MetricsEnabled    bool                        `json:"metrics_enabled"`
	MetricsInterval   int                         `json:"metrics_interval_seconds"`
	HeartbeatEnabled  bool                        `json:"heartbeat_enabled"`
	HeartbeatInterval int                         `json:"heartbeat_interval_seconds"`
	RemoteManagement  bool                        `json:"remote_management_enabled"`
	Permissions       RemoteManagementPermissions `json:"remote_management_permissions"`
}

// DefaultConfig son los valores con los que arranca una máquina nueva:
// remote management y todos sus permisos apagados — el usuario los prende
// a mano si quiere (spec §28: "instalar el agente NO debe significar
// automáticamente que Cloud tiene control total").
func DefaultConfig() Config {
	return Config{
		ServiceBind:       "127.0.0.1",
		ServicePort:       8091,
		MetricsEnabled:    true,
		MetricsInterval:   60,
		HeartbeatEnabled:  true,
		HeartbeatInterval: 30,
		RemoteManagement:  false,
	}
}

func configPath() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(base, "asterion")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return filepath.Join(dir, "runtime.json"), nil
}

// LoadConfig lee la config guardada, o devuelve DefaultConfig() si todavía
// no se guardó ninguna — nunca falla solo porque el archivo no existe.
func LoadConfig() (Config, error) {
	cfg := DefaultConfig()
	path, err := configPath()
	if err != nil {
		return cfg, err
	}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return cfg, nil
	}
	if err != nil {
		return cfg, err
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return cfg, err
	}
	return cfg, nil
}

func SaveConfig(cfg Config) error {
	path, err := configPath()
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}
